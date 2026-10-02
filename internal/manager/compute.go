// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// containerInitBin hardens the entrypoint (drops capabilities, applies a seccomp
// filter) before executing a non-privileged workload.
const containerInitBin = "/usr/bin/infra-container-init"

// ComputeMount attaches a host device or backing file to a path in the rootfs.
type ComputeMount struct {
	Source   string
	Target   string
	ReadOnly bool
}

// ComputeRequest is a fully resolved compute instance to realize on the host:
// the reconciler has already resolved the bridge, an address, the subnet gateway
// and any disk device paths.
type ComputeRequest struct {
	UID        string
	Hostname   string
	Image      string
	Command    string
	Env        map[string]string
	Ports      []string
	CPU        float64
	MemoryMB   int
	PidsMax    int
	Privileged bool
	VPCID      string
	Bridge     string
	IP         string
	Prefix     int
	Gateway    string
	DNS        string
	SGChain    string
	Disks      []ComputeMount
}

// ComputeResult reports the realized topology of a compute instance.
type ComputeResult struct {
	Namespace string
	VethHost  string
	Rootfs    string
}

// ComputeTeardown is the information needed to tear an instance down without its
// live spec, reconstructed from status and spec by the reconciler.
type ComputeTeardown struct {
	UID     string
	VPCID   string
	Bridge  string
	IP      string
	Ports   []string
	SGChain string
	Rootfs  string
	Disks   []ComputeMount
}

// ComputeBackend abstracts the host operations a compute instance needs.
type ComputeBackend interface {
	// EnsureCompute realizes req and returns its topology. It creates the
	// instance once: a subsequent call for an existing namespace is a no-op.
	EnsureCompute(ctx context.Context, req ComputeRequest) (ComputeResult, error)
	// DeleteCompute tears an instance down. Tearing down an absent instance is
	// not an error.
	DeleteCompute(ctx context.Context, td ComputeTeardown) error
}

// computeNames derives the namespace and veth interface names for a UID. The
// veth names stay within the kernel interface-name limit.
func computeNames(uid string) (ns, vethHost, vethNS string) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(uid))
	s := fmt.Sprintf("%08x", h.Sum32())
	return "ns-" + s, "vh-" + s, "vp-" + s
}

// ExecComputeBackend realizes compute instances as network-namespaced containers
// using iproute2, iptables, cgroup v2 and an OCI rootfs. It requires root.
type ExecComputeBackend struct {
	run      Runner
	images   *imagePuller
	cgroups  *cgroupManager
	netnsDir string
	stateDir string
}

// NewExecComputeBackend stores rootfs, image cache and entry scripts under
// stateDir and writes per-namespace resolv.conf under /etc/netns.
func NewExecComputeBackend(stateDir string) *ExecComputeBackend {
	return &ExecComputeBackend{
		run:      defaultRun,
		images:   newImagePuller(stateDir),
		cgroups:  newCgroupManager(""),
		netnsDir: "/etc/netns",
		stateDir: stateDir,
	}
}

// NewExecComputeBackendWithRunner builds a backend with overridable directories
// and runner, used in tests to assert the issued commands without touching the
// host.
func NewExecComputeBackendWithRunner(stateDir, netnsDir, cgroupBase string, run Runner) *ExecComputeBackend {
	return &ExecComputeBackend{
		run:      run,
		images:   newImagePuller(stateDir),
		cgroups:  newCgroupManager(cgroupBase),
		netnsDir: netnsDir,
		stateDir: stateDir,
	}
}

// EnsureCompute keeps the applied instance only while its configuration and
// host launcher identity match. Drift, an exited workload or missing runtime
// state causes a teardown and recreation. Failed creation is cleaned up so a
// namespace left behind is never treated as a running workload.
func (b *ExecComputeBackend) EnsureCompute(ctx context.Context, req ComputeRequest) (res ComputeResult, err error) {
	ns, vethHost, vethNS := computeNames(req.UID)
	res = ComputeResult{Namespace: ns, VethHost: vethHost}
	if req.Image != "" {
		res.Rootfs = b.images.rootfsPath(req.UID)
	}

	exists, err := b.netnsExists(ctx, ns)
	if err != nil {
		return res, err
	}
	if exists {
		snapshot, loadErr := b.loadComputeState(req.UID)
		if loadErr == nil && reflect.DeepEqual(snapshot.Request, req) && (req.Image == "" || processMatches(snapshot.PID, snapshot.StartTime)) {
			return res, b.ensureFirewall(ctx, req)
		}
		previous := req
		if loadErr == nil {
			previous = snapshot.Request
		}
		if err := b.DeleteCompute(ctx, teardownOf(previous, res.Rootfs)); err != nil {
			return res, err
		}
	}

	defer func() {
		if err != nil {
			_ = b.DeleteCompute(ctx, teardownOf(req, res.Rootfs))
		}
	}()
	if err := b.setupNetwork(ctx, req, ns, vethHost, vethNS); err != nil {
		return res, err
	}
	if err := b.writeResolv(ns, req.DNS); err != nil {
		return res, err
	}
	if err := b.ensureFirewall(ctx, req); err != nil {
		return res, err
	}
	if req.CPU > 0 || req.MemoryMB > 0 || req.PidsMax > 0 {
		if err := b.cgroups.setup(req.UID, req.CPU, req.MemoryMB, req.PidsMax); err != nil {
			return res, err
		}
	}
	if req.Image != "" {
		rootfs, err := b.images.pullAndExtract(req.Image, req.UID)
		if err != nil {
			return res, err
		}
		res.Rootfs = rootfs
		b.copyResolvIntoRootfs(ns, rootfs)
	}
	for _, d := range req.Disks {
		target := d.Target
		if res.Rootfs != "" {
			target = filepath.Join(res.Rootfs, d.Target)
		}
		if err := b.mountDisk(ctx, d, target); err != nil {
			return res, err
		}
	}

	cmd := req.Command
	if req.Image != "" && cmd == "" {
		if cfg, cfgErr := b.images.config(req.Image); cfgErr == nil {
			cmd = joinEntrypoint(cfg)
		}
	}
	snapshot := computeState{Request: req}
	if res.Rootfs != "" {
		if cmd == "" {
			return res, fmt.Errorf("manager: image has no entrypoint")
		}
		if !req.Privileged {
			if _, err := os.Stat(containerInitBin); err != nil {
				return res, fmt.Errorf("manager: container-init is required for unprivileged compute: %w", err)
			}
		}
		pid, start, startErr := b.startEntrypoint(req, ns, res.Rootfs, cmd)
		if startErr != nil {
			return res, startErr
		}
		snapshot.PID, snapshot.StartTime = pid, start
	}
	if err := b.saveComputeState(req.UID, snapshot); err != nil {
		return res, err
	}
	return res, nil
}

// netnsExists reports whether the named network namespace is present.
func (b *ExecComputeBackend) netnsExists(ctx context.Context, ns string) (bool, error) {
	out, err := b.run(ctx, "ip", "netns", "list")
	if err != nil {
		return false, fmt.Errorf("manager: list netns: %w: %s", err, strings.TrimSpace(out))
	}
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == ns {
			return true, nil
		}
	}
	return false, nil
}

// setupNetwork creates the namespace, the veth pair, attaches it to the bridge
// and assigns the address and default route inside the namespace.
func (b *ExecComputeBackend) setupNetwork(ctx context.Context, req ComputeRequest, ns, vethHost, vethNS string) error {
	addr := req.IP + "/" + strconv.Itoa(req.Prefix)
	steps := [][]string{
		{"netns", "add", ns},
		// overlayMTU on both ends, so what the instance sends still fits once
		// the VPC overlay encapsulates it for another host.
		{"link", "add", vethHost, "mtu", strconv.Itoa(overlayMTU), "type", "veth", "peer", "name", vethNS, "mtu", strconv.Itoa(overlayMTU)},
		{"link", "set", vethHost, "master", req.Bridge},
		{"link", "set", vethHost, "up"},
		{"link", "set", vethNS, "netns", ns},
		{"netns", "exec", ns, "ip", "link", "set", "lo", "up"},
		{"netns", "exec", ns, "ip", "link", "set", vethNS, "up"},
		{"netns", "exec", ns, "ip", "addr", "add", addr, "dev", vethNS},
		{"netns", "exec", ns, "ip", "route", "add", "default", "via", req.Gateway},
	}
	for _, s := range steps {
		if out, err := b.run(ctx, "ip", s...); err != nil {
			return fmt.Errorf("manager: compute network %v: %w: %s", s, err, strings.TrimSpace(out))
		}
	}
	return nil
}

// writeResolv writes the namespace's resolv.conf, used inside the namespace.
func (b *ExecComputeBackend) writeResolv(ns, dns string) error {
	dir := filepath.Join(b.netnsDir, ns)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("manager: netns dir: %w", err)
	}
	content := "nameserver 8.8.8.8\n"
	if dns != "" {
		content = "nameserver " + dns + "\nnameserver 8.8.8.8\n"
	}
	// #nosec G306 -- resolv.conf must be readable by the contained resolver
	if err := os.WriteFile(filepath.Join(dir, "resolv.conf"), []byte(content), 0o644); err != nil {
		return fmt.Errorf("manager: write resolv.conf: %w", err)
	}
	return nil
}

// copyResolvIntoRootfs mirrors the namespace resolv.conf into the rootfs.
func (b *ExecComputeBackend) copyResolvIntoRootfs(ns, rootfs string) {
	src := filepath.Join(b.netnsDir, ns, "resolv.conf")
	data, err := os.ReadFile(src) // #nosec G304 -- path under the agent-owned netns dir
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Join(rootfs, "etc"), 0o750)
	// #nosec G306 G703 -- rootfs is an agent-derived path under the state dir;
	// resolv.conf must be readable by the contained resolver.
	_ = os.WriteFile(filepath.Join(rootfs, "etc", "resolv.conf"), data, 0o644)
}

// computeRules returns the iptables rules of an instance, each as its table
// and the rule's chain and matches: the security group's chain for what
// reaches the instance through its VPC's bridge - two VPCs may number an
// instance alike, so the address alone does not tell them apart, and what the
// host itself sends goes through OUTPUT first on the VRF device, untracked,
// where the group's ESTABLISHED rule could not let a reply through - and for
// each port it maps, the DNAT from the host port, its acceptance, and the
// marks routing it into the VPC's table and its replies back to the main one
// (see vrf.go).
func computeRules(vpcID, bridge, ip, sgChain string, ports []string) [][]string {
	var rules [][]string
	if sgChain != "" {
		rules = append(rules,
			[]string{"filter", "FORWARD", "-o", bridge, "-d", ip, "-j", sgChain},
			[]string{"filter", "OUTPUT", "-o", bridge, "-d", ip, "-j", sgChain},
		)
	}
	for _, p := range ports {
		host, container, proto, err := parsePortMapping(p)
		if err != nil {
			continue
		}
		dest := ip + ":" + container
		rules = append(rules,
			[]string{"mangle", "PREROUTING", "!", "-i", bridge, "-p", proto, "--dport", host,
				"-m", "conntrack", "--ctstate", "NEW", "-j", "CONNMARK", "--set-mark", hex32(vrfMainMark(vpcID))},
			[]string{"mangle", "PREROUTING", "!", "-i", bridge, "-p", proto, "--dport", host,
				"-j", "MARK", "--set-mark", hex32(vrfMark(vpcID))},
			[]string{"nat", "PREROUTING", "-p", proto, "--dport", host, "-j", "DNAT", "--to-destination", dest},
			[]string{"nat", "OUTPUT", "-p", proto, "--dport", host, "-j", "DNAT", "--to-destination", dest},
			[]string{"filter", "FORWARD", "-o", bridge, "-p", proto, "-d", ip, "--dport", container, "-j", "ACCEPT"},
		)
	}
	return rules
}

// legacyComputeRules are the rules an agent before the VPC's VRF wrote, which
// matched the instance by address alone.
func legacyComputeRules(ip, sgChain string, ports []string) [][]string {
	var rules [][]string
	if sgChain != "" {
		rules = append(rules,
			[]string{"filter", "FORWARD", "-d", ip, "-j", sgChain},
			[]string{"filter", "OUTPUT", "-d", ip, "-j", sgChain},
		)
	}
	for _, p := range ports {
		if _, container, proto, err := parsePortMapping(p); err == nil {
			rules = append(rules, []string{"filter", "FORWARD", "-p", proto, "-d", ip, "--dport", container, "-j", "ACCEPT"})
		}
	}
	return rules
}

// ensureFirewall puts the instance's rules in place and removes the ones an
// older agent wrote.
func (b *ExecComputeBackend) ensureFirewall(ctx context.Context, req ComputeRequest) error {
	for _, r := range legacyComputeRules(req.IP, req.SGChain, req.Ports) {
		iptablesDelete(ctx, b.run, r[0], r[1:])
	}
	for _, r := range computeRules(req.VPCID, req.Bridge, req.IP, req.SGChain, req.Ports) {
		if err := iptablesEnsure(ctx, b.run, r[0], r[1:]); err != nil {
			return err
		}
	}
	return nil
}

// mountDisk mounts a disk source at target inside the rootfs, looping a backing
// file and applying read-only when requested.
func (b *ExecComputeBackend) mountDisk(ctx context.Context, m ComputeMount, target string) error {
	if err := os.MkdirAll(target, 0o750); err != nil {
		return fmt.Errorf("manager: mount target %q: %w", target, err)
	}
	var opts []string
	if !strings.HasPrefix(m.Source, "/dev/") {
		opts = append(opts, "loop")
	}
	if m.ReadOnly {
		opts = append(opts, "ro")
	}
	args := []string{}
	if len(opts) > 0 {
		args = append(args, "-o", strings.Join(opts, ","))
	}
	args = append(args, m.Source, target)
	if out, err := b.run(ctx, "mount", args...); err != nil {
		return fmt.Errorf("manager: mount %s at %s: %w: %s", m.Source, target, err, strings.TrimSpace(out))
	}
	return nil
}

// DeleteCompute tears an instance down, best effort.
func (b *ExecComputeBackend) DeleteCompute(ctx context.Context, td ComputeTeardown) error {
	ns, vethHost, _ := computeNames(td.UID)

	if data, err := os.ReadFile(b.cgroups.procsFile(td.UID)); err == nil { // #nosec G304 -- agent-owned cgroup path
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line != "" {
				_, _ = b.run(ctx, "kill", "-9", line)
			}
		}
	}
	if out, err := b.run(ctx, "ip", "netns", "pids", ns); err == nil {
		for _, pid := range strings.Fields(out) {
			_, _ = b.run(ctx, "kill", "-9", pid)
		}
	}
	if td.IP != "" {
		for _, r := range legacyComputeRules(td.IP, td.SGChain, td.Ports) {
			iptablesDelete(ctx, b.run, r[0], r[1:])
		}
		if td.Bridge != "" {
			for _, r := range computeRules(td.VPCID, td.Bridge, td.IP, td.SGChain, td.Ports) {
				iptablesDelete(ctx, b.run, r[0], r[1:])
			}
		}
	}
	for _, d := range td.Disks {
		target := d.Target
		if td.Rootfs != "" {
			target = filepath.Join(td.Rootfs, d.Target)
		}
		_, _ = b.run(ctx, "umount", "-l", target)
	}
	_, _ = b.run(ctx, "ip", "link", "del", vethHost)
	_, _ = b.run(ctx, "ip", "netns", "del", ns)
	_ = os.RemoveAll(filepath.Join(b.netnsDir, ns))
	_ = b.cgroups.remove(td.UID)
	_ = b.images.cleanupRootfs(td.UID)

	_ = os.Remove(filepath.Join(b.stateDir, "compute", td.UID+".entry.sh"))
	_ = os.Remove(filepath.Join(b.stateDir, "compute", td.UID+".entry.pid"))
	_ = os.Remove(filepath.Join(b.stateDir, "compute", td.UID+".runtime.json"))
	if matches, _ := filepath.Glob(filepath.Join(b.stateDir, "compute", td.UID+"*.entry.log")); matches != nil {
		for _, m := range matches {
			_ = os.Remove(m)
		}
	}
	return nil
}

// startEntrypoint launches the image's process inside the namespace with
// pivot_root isolation in its own mount and PID namespaces, placed in the
// instance cgroup. It runs detached so it survives the agent.
func (b *ExecComputeBackend) startEntrypoint(req ComputeRequest, ns, rootfs, entryCmd string) (int, string, error) {
	dir := filepath.Join(b.stateDir, "compute")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return 0, "", err
	}
	scriptPath := filepath.Join(dir, req.UID+".entry.sh")
	pidFile := filepath.Join(dir, req.UID+".entry.pid")
	logName := req.UID + ".entry"
	if req.Hostname != "" {
		logName = req.UID + "." + req.Hostname + ".entry"
	}
	logFile := filepath.Join(dir, logName+".log")

	shell := rootfsShell(rootfs)
	if shell == "" {
		return 0, "", fmt.Errorf("manager: rootfs has no shell")
	}
	_, hasInit := os.Stat(containerInitBin)
	hasContainerInit := hasInit == nil
	cgroupProcs := b.cgroups.procsFile(req.UID)
	_, cgErr := os.Stat(filepath.Dir(cgroupProcs))
	hasCgroup := cgErr == nil

	script := buildEntryScript(req, rootfs, entryCmd, shell, pidFile, cgroupProcs, hasCgroup, hasContainerInit)
	// #nosec G306 -- the entry wrapper must be executable to be run by `ip netns exec ... sh`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return 0, "", err
	}

	args := []string{"netns", "exec", ns, "unshare", "--mount", "--pid", "--fork"}
	if req.Hostname != "" {
		args = append(args, "--uts")
	}
	args = append(args, "sh", scriptPath)

	// #nosec G204 -- the agent launches the contained process with arguments it
	// controls (namespace name and its own script path), not external input.
	cmd := exec.Command("ip", args...)
	logf, err := os.Create(logFile) // #nosec G304 -- agent-owned log path
	if err == nil {
		cmd.Stdout = logf
		cmd.Stderr = logf
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if fd, err := syscall.Open(b.cgroups.path(req.UID), syscall.O_RDONLY|syscall.O_DIRECTORY, 0); err == nil {
		cmd.SysProcAttr.UseCgroupFD = true
		cmd.SysProcAttr.CgroupFD = fd
		defer func() { _ = syscall.Close(fd) }()
	}
	if err := cmd.Start(); err != nil {
		if logf != nil {
			_ = logf.Close()
		}
		return 0, "", err
	}
	go func() {
		_ = cmd.Wait()
		if logf != nil {
			_ = logf.Close()
		}
	}()
	pid := cmd.Process.Pid
	start, err := processStartTime(pid)
	if err != nil {
		_ = cmd.Process.Kill()
		return 0, "", err
	}
	time.Sleep(100 * time.Millisecond)
	if !processMatches(pid, start) {
		return 0, "", fmt.Errorf("manager: workload exited during startup")
	}
	return pid, start, nil
}

// rootfsShell returns the first usable POSIX shell found inside rootfs.
func rootfsShell(rootfs string) string {
	for _, candidate := range []string{"/bin/sh", "/bin/ash", "/bin/bash"} {
		if info, err := os.Stat(filepath.Join(rootfs, candidate)); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return candidate
		}
	}
	return ""
}

func shellArgument(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

// buildEntryScript assembles the shell wrapper that mounts the special
// filesystems, pivot_roots into rootfs and execs the entrypoint.
func buildEntryScript(req ComputeRequest, rootfs, entryCmd, shell, pidFile, cgroupProcs string, hasCgroup, hasContainerInit bool) string {
	var s strings.Builder
	s.WriteString("#!/bin/sh\nset -eu\nmount --make-rprivate /\n")
	_ = pidFile // Host-side supervision records the launcher PID, not namespace PID 1.
	if hasCgroup {
		fmt.Fprintf(&s, "echo $$ > %s 2>/dev/null\n", cgroupProcs)
	}
	if req.Hostname != "" {
		fmt.Fprintf(&s, "hostname %s 2>/dev/null\n", shellArgument(req.Hostname))
	}
	if len(req.Env) > 0 {
		keys := make([]string, 0, len(req.Env))
		for k := range req.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&s, "export %s=%s\n", shellArgument(k), shellArgument(req.Env[k]))
		}
	}
	fmt.Fprintf(&s, "mkdir -p %s/proc %s/sys %s/dev 2>/dev/null\n", rootfs, rootfs, rootfs)
	fmt.Fprintf(&s, "mount -t proc proc %s/proc 2>/dev/null\n", rootfs)
	if req.Privileged {
		fmt.Fprintf(&s, "mount --rbind /sys %s/sys\n", rootfs)
		fmt.Fprintf(&s, "mount --rbind /dev %s/dev\n", rootfs)
	} else {
		fmt.Fprintf(&s, "mount -t sysfs -o ro,nosuid,nodev,noexec sysfs %s/sys\n", rootfs)
		fmt.Fprintf(&s, "mount -t tmpfs -o nosuid,noexec,mode=755 tmpfs %s/dev\n", rootfs)
		for _, dev := range []struct {
			name         string
			major, minor int
		}{{"null", 1, 3}, {"zero", 1, 5}, {"full", 1, 7}, {"random", 1, 8}, {"urandom", 1, 9}, {"tty", 5, 0}} {
			fmt.Fprintf(&s, "mknod -m 666 %s/dev/%s c %d %d\n", rootfs, dev.name, dev.major, dev.minor)
		}
		fmt.Fprintf(&s, "mkdir -p %s/dev/pts\nmount -t devpts -o newinstance,nosuid,noexec,mode=620,ptmxmode=666 devpts %s/dev/pts\nln -s pts/ptmx %s/dev/ptmx\n", rootfs, rootfs, rootfs)
	}
	if hasContainerInit {
		fmt.Fprintf(&s, "mkdir -p %s/usr/bin 2>/dev/null\n", rootfs)
		fmt.Fprintf(&s, "cp %s %s/usr/bin/infra-container-init 2>/dev/null\n", containerInitBin, rootfs)
	}
	fmt.Fprintf(&s, "mount --rbind %s %s\n", rootfs, rootfs)
	fmt.Fprintf(&s, "cd %s\n", rootfs)
	s.WriteString("mkdir -p .old_root\n")
	s.WriteString("pivot_root . .old_root\n")
	s.WriteString("cd /\n")
	s.WriteString("umount -l /.old_root 2>/dev/null\n")
	s.WriteString("rmdir /.old_root 2>/dev/null\n")
	if req.Privileged {
		s.WriteString("mkdir -p /sys/fs/cgroup 2>/dev/null\n")
		s.WriteString("mount -t cgroup2 none /sys/fs/cgroup 2>/dev/null || true\n")
	}
	quoted := strings.ReplaceAll(entryCmd, "'", "'\\''")
	switch {
	case !req.Privileged && hasContainerInit && shell != "":
		fmt.Fprintf(&s, "exec %s -- %s -c '%s'\n", containerInitBin, shell, quoted)
	case !req.Privileged && hasContainerInit:
		fmt.Fprintf(&s, "exec %s -- %s\n", containerInitBin, entryCmd)
	case shell != "":
		fmt.Fprintf(&s, "exec %s -c '%s'\n", shell, quoted)
	default:
		fmt.Fprintf(&s, "exec %s\n", entryCmd)
	}
	return s.String()
}

// joinEntrypoint renders an image's entrypoint and command as a shell string.
func joinEntrypoint(cfg *imageConfig) string {
	parts := append(append([]string{}, cfg.Entrypoint...), cfg.Cmd...)
	if len(parts) == 0 {
		return ""
	}
	quoted := make([]string, len(parts))
	for i, p := range parts {
		if strings.ContainsAny(p, " \t'\";&|") {
			quoted[i] = strconv.Quote(p)
		} else {
			quoted[i] = p
		}
	}
	return strings.Join(quoted, " ")
}

// parsePortMapping parses "hostPort:containerPort[/proto]" into its parts.
func parsePortMapping(spec string) (host, container, proto string, err error) {
	proto = "tcp"
	portPart := spec
	if idx := strings.LastIndex(spec, "/"); idx != -1 {
		proto = spec[idx+1:]
		portPart = spec[:idx]
	}
	parts := strings.SplitN(portPart, ":", 2)
	if len(parts) != 2 {
		return "", "", "", fmt.Errorf("manager: port mapping %q: want hostPort:containerPort", spec)
	}
	host = strings.TrimSpace(parts[0])
	container = strings.TrimSpace(parts[1])
	if _, e := strconv.Atoi(host); e != nil {
		return "", "", "", fmt.Errorf("manager: port mapping %q: invalid host port", spec)
	}
	if _, e := strconv.Atoi(container); e != nil {
		return "", "", "", fmt.Errorf("manager: port mapping %q: invalid container port", spec)
	}
	if proto != "tcp" && proto != "udp" {
		return "", "", "", fmt.Errorf("manager: port mapping %q: protocol must be tcp or udp", spec)
	}
	return host, container, proto, nil
}

// ComputeRegistry is the typed store the compute reconciler reads and writes.
type ComputeRegistry = registry.Registry[resource.ComputeSpec, resource.ComputeStatus]

// ComputeReconciler realizes a compute instance: it resolves the subnet, VPC
// bridge, an address, any disks and the security-group chain, then asks the
// backend to bring the namespaced container up.
type ComputeReconciler struct {
	reg     *ComputeRegistry
	subnets *SubnetRegistry
	vpcs    *VPCRegistry
	disks   *DiskRegistry
	sgs     *SecurityGroupRegistry
	backend ComputeBackend
	// nodeName scopes realization to compute scheduled to this node. When empty
	// the reconciler realizes every compute (single-host mode).
	nodeName string
	// addresses reserves instance addresses in the shared store; nil falls
	// back to picking one no other instance lists, which is only safe with a
	// single agent.
	addresses *addressBook
}

// NewComputeReconciler returns a reconciler backed by the resource stores and
// the compute backend. nodeName scopes realization to compute scheduled to this
// node; an empty nodeName realizes every compute.
func NewComputeReconciler(reg *ComputeRegistry, subnets *SubnetRegistry, vpcs *VPCRegistry, disks *DiskRegistry, sgs *SecurityGroupRegistry, backend ComputeBackend, nodeName string) *ComputeReconciler {
	return &ComputeReconciler{reg: reg, subnets: subnets, vpcs: vpcs, disks: disks, sgs: sgs, backend: backend, nodeName: nodeName}
}

// WithAddressStore makes the reconciler reserve each instance address in
// store before using it, so agents allocating at once on different hosts
// never hand out the same one.
func (r *ComputeReconciler) WithAddressStore(store state.Store) *ComputeReconciler {
	r.addresses = &addressBook{store: store}
	return r
}

// Name identifies the reconcile pass.
func (r *ComputeReconciler) Name() string { return resource.KindCompute }

// ReconcileAll reconciles every compute instance, collecting per-instance errors.
func (r *ComputeReconciler) ReconcileAll(ctx context.Context) error {
	computes, err := r.reg.WithContext(ctx).List()
	if err != nil {
		return fmt.Errorf("manager: list computes: %w", err)
	}
	var errs []error
	for i := range computes {
		uid := computes[i].Metadata.UID
		if err := r.Reconcile(ctx, uid); err != nil {
			errs = append(errs, fmt.Errorf("compute %s: %w", uid, err))
		}
	}
	return errors.Join(errs...)
}

// Reconcile brings the compute identified by uid in line with its spec.
func (r *ComputeReconciler) Reconcile(ctx context.Context, uid string) error {
	c, err := r.reg.WithContext(ctx).Get(uid)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("manager: load compute %q: %w", uid, err)
	}
	if r.nodeName != "" && c.Status.NodeName != r.nodeName {
		// Scheduled to another node (or not yet scheduled); leave it alone.
		return nil
	}
	if c.Metadata.IsDeleting() {
		return r.finalize(ctx, c)
	}
	return r.ensure(ctx, c)
}

func (r *ComputeReconciler) ensure(ctx context.Context, c *resource.Compute) error {
	if !c.Metadata.HasFinalizer(resource.ComputeFinalizer) {
		c.Metadata.AddFinalizer(resource.ComputeFinalizer)
	}

	req, ready, err := r.resolve(c)
	if err != nil {
		c.Status.SetPhase(resource.PhaseError, "ResolveError", err.Error())
		_ = r.reg.WithContext(ctx).Put(c)
		return err
	}
	if !ready {
		// A dependency is not provisioned yet; retry on the next pass.
		return r.reg.WithContext(ctx).Put(c)
	}

	// Record the address before realising the instance, so a failed attempt
	// retries with the one it already holds instead of reserving another.
	c.Status.IP = req.IP
	c.Status.SetPhase(resource.PhaseReconciling, "Reconciling", "starting instance")
	res, err := r.backend.EnsureCompute(ctx, req)
	if err != nil {
		c.Status.SetPhase(resource.PhaseError, "ComputeError", err.Error())
		_ = r.reg.WithContext(ctx).Put(c)
		return err
	}

	c.Status.IP = req.IP
	c.Status.Namespace = res.Namespace
	c.Status.VethHost = res.VethHost
	c.Status.Rootfs = res.Rootfs
	c.Status.Ready = true
	c.Status.MarkReconciled(c.Metadata.Generation)
	c.Status.SetPhase(resource.PhaseReady, "Running", "instance ready")
	if err := r.reg.WithContext(ctx).Put(c); err != nil {
		return fmt.Errorf("manager: save compute %q: %w", c.Metadata.UID, err)
	}
	return nil
}

// resolve gathers the realized request for a compute. ready is false when a
// dependency (subnet gateway, VPC bridge, disk, security group) is not ready,
// signalling the caller to requeue without an error.
func (r *ComputeReconciler) resolve(c *resource.Compute) (ComputeRequest, bool, error) {
	subnet, err := r.subnets.Get(c.Spec.SubnetID)
	if errors.Is(err, state.ErrNotFound) {
		return ComputeRequest{}, false, fmt.Errorf("subnet %q not found", c.Spec.SubnetID)
	}
	if err != nil {
		return ComputeRequest{}, false, err
	}
	if subnet.Status.Gateway == "" {
		c.Status.SetPhase(resource.PhasePending, "WaitingForSubnet", "subnet gateway not ready")
		return ComputeRequest{}, false, nil
	}

	vpc, err := r.vpcs.Get(subnet.Spec.VPCID)
	if errors.Is(err, state.ErrNotFound) {
		return ComputeRequest{}, false, fmt.Errorf("vpc %q not found", subnet.Spec.VPCID)
	}
	if err != nil {
		return ComputeRequest{}, false, err
	}
	if vpc.Status.BridgeName == "" {
		c.Status.SetPhase(resource.PhasePending, "WaitingForVPC", "vpc bridge not ready")
		return ComputeRequest{}, false, nil
	}

	sgChain := ""
	if c.Spec.SecurityGroupID != "" {
		sg, sgErr := r.sgs.Get(c.Spec.SecurityGroupID)
		if errors.Is(sgErr, state.ErrNotFound) {
			return ComputeRequest{}, false, fmt.Errorf("security group %q not found", c.Spec.SecurityGroupID)
		}
		if sgErr != nil {
			return ComputeRequest{}, false, sgErr
		}
		if sg.Status.Chain == "" {
			c.Status.SetPhase(resource.PhasePending, "WaitingForSG", "security-group chain not ready")
			return ComputeRequest{}, false, nil
		}
		sgChain = sg.Status.Chain
	}

	var mounts []ComputeMount
	for _, d := range c.Spec.Disks {
		disk, dErr := r.disks.Get(d.DiskID)
		if errors.Is(dErr, state.ErrNotFound) {
			return ComputeRequest{}, false, fmt.Errorf("disk %q not found", d.DiskID)
		}
		if dErr != nil {
			return ComputeRequest{}, false, dErr
		}
		if disk.Status.Path == "" {
			c.Status.SetPhase(resource.PhasePending, "WaitingForDisk", "disk not provisioned")
			return ComputeRequest{}, false, nil
		}
		mounts = append(mounts, ComputeMount{Source: disk.Status.Path, Target: d.MountPath, ReadOnly: d.ReadOnly})
	}

	ip := c.Status.IP
	switch {
	case ip == "":
		used, uErr := r.usedIPs(c.Metadata.UID)
		if uErr != nil {
			return ComputeRequest{}, false, uErr
		}
		used[subnet.Status.Gateway] = true
		if r.addresses != nil {
			ip, err = r.addresses.allocate(subnet.Spec.CIDR, c.Spec.SubnetID, c.Metadata.UID, used)
		} else {
			ip, err = allocateIP(subnet.Spec.CIDR, used)
		}
		if err != nil {
			return ComputeRequest{}, false, err
		}
	case r.addresses != nil:
		// An address given before reservations existed, or kept across a
		// restart: hold it, unless another instance already does.
		ok, owner, cErr := r.addresses.claim(c.Spec.SubnetID, ip, c.Metadata.UID)
		if cErr != nil {
			return ComputeRequest{}, false, cErr
		}
		if !ok {
			return ComputeRequest{}, false, fmt.Errorf("address %s is reserved by %s; recreate this instance to get another", ip, owner)
		}
	}

	return ComputeRequest{
		UID:        c.Metadata.UID,
		Hostname:   c.Spec.Hostname,
		Image:      c.Spec.Image,
		Command:    c.Spec.Command,
		Env:        c.Spec.Env,
		Ports:      c.Spec.Ports,
		CPU:        c.Spec.CPU,
		MemoryMB:   c.Spec.MemoryMB,
		PidsMax:    c.Spec.PidsMax,
		Privileged: c.Spec.Privileged,
		VPCID:      vpc.Metadata.UID,
		Bridge:     vpc.Status.BridgeName,
		IP:         ip,
		Prefix:     prefixLen(subnet.Spec.CIDR),
		Gateway:    subnet.Status.Gateway,
		DNS:        firstHostOf(vpc.Spec.CIDR),
		SGChain:    sgChain,
		Disks:      mounts,
	}, true, nil
}

func (r *ComputeReconciler) finalize(ctx context.Context, c *resource.Compute) error {
	if c.Metadata.HasFinalizer(resource.ComputeFinalizer) {
		vpcID := r.vpcOf(c.Spec.SubnetID)
		td := ComputeTeardown{
			UID:     c.Metadata.UID,
			VPCID:   vpcID,
			IP:      c.Status.IP,
			Ports:   c.Spec.Ports,
			Rootfs:  c.Status.Rootfs,
			SGChain: r.sgChain(c.Spec.SecurityGroupID),
		}
		if vpcID != "" {
			td.Bridge = bridgeName(vpcID)
		}
		for _, d := range c.Spec.Disks {
			td.Disks = append(td.Disks, ComputeMount{Target: d.MountPath})
		}
		if err := r.backend.DeleteCompute(ctx, td); err != nil {
			c.Status.SetPhase(resource.PhaseError, "ComputeError", err.Error())
			_ = r.reg.WithContext(ctx).Put(c)
			return err
		}
		if r.addresses != nil && c.Status.IP != "" {
			if err := r.addresses.release(c.Spec.SubnetID, c.Status.IP, c.Metadata.UID); err != nil {
				return err
			}
		}
		c.Metadata.RemoveFinalizer(resource.ComputeFinalizer)
		c.Status.SetPhase(resource.PhaseDeleting, "Deleting", "instance removed")
		if err := r.reg.WithContext(ctx).Put(c); err != nil {
			return fmt.Errorf("manager: save compute %q: %w", c.Metadata.UID, err)
		}
	}
	if len(c.Metadata.Finalizers) == 0 {
		if err := r.reg.WithContext(ctx).Delete(c.Metadata.UID); err != nil {
			return fmt.Errorf("manager: delete compute %q: %w", c.Metadata.UID, err)
		}
	}
	return nil
}

// vpcOf returns the VPC of a subnet, empty if it cannot be resolved (best
// effort, used during teardown).
func (r *ComputeReconciler) vpcOf(subnetID string) string {
	if sn, err := r.subnets.Get(subnetID); err == nil {
		return sn.Spec.VPCID
	}
	return ""
}

// sgChain returns the iptables chain of a security group, empty if it cannot be
// resolved (best effort, used during teardown).
func (r *ComputeReconciler) sgChain(sgID string) string {
	if sgID == "" {
		return ""
	}
	if sg, err := r.sgs.Get(sgID); err == nil {
		return sg.Status.Chain
	}
	return ""
}

// usedIPs returns the addresses already assigned to other compute instances.
func (r *ComputeReconciler) usedIPs(exclude string) (map[string]bool, error) {
	list, err := r.reg.List()
	if err != nil {
		return nil, fmt.Errorf("manager: list computes: %w", err)
	}
	used := make(map[string]bool)
	for i := range list {
		if list[i].Metadata.UID == exclude {
			continue
		}
		if ip := list[i].Status.IP; ip != "" {
			used[ip] = true
		}
	}
	return used, nil
}

// maxNodePorts is how many hosts get an address of their own in each subnet
// (see nodeAddress). allocateIP never hands those addresses out.
const maxNodePorts = 5

// nodeAddress returns the address the host ranked rank holds in cidr on its
// node port: counting down from the last usable host, so the addresses stay
// clear of allocateIP, which counts up from .10.
func nodeAddress(cidr string, rank int) (string, error) {
	if rank < 0 || rank >= maxNodePorts {
		return "", fmt.Errorf("manager: node rank %d has no address: only %d hosts get one per subnet", rank, maxNodePorts)
	}
	last, ipnet, err := lastHost(cidr)
	if err != nil {
		return "", err
	}
	addr := last - uint32(rank)                                               // #nosec G115 -- rank is bounded to [0, maxNodePorts) above
	ip := net.IPv4(byte(addr>>24), byte(addr>>16), byte(addr>>8), byte(addr)) // #nosec G115 -- extracting octets of a uint32
	if !ipnet.Contains(ip) {
		return "", fmt.Errorf("manager: subnet %s is too small for node addresses", cidr)
	}
	return ip.String(), nil
}

// lastHost returns the last usable IPv4 host of cidr as an integer.
func lastHost(cidr string) (uint32, *net.IPNet, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return 0, nil, fmt.Errorf("manager: invalid cidr %q: %w", cidr, err)
	}
	base := ipnet.IP.To4()
	if base == nil {
		return 0, nil, fmt.Errorf("manager: only IPv4 subnets are supported: %q", cidr)
	}
	ones, bits := ipnet.Mask.Size()
	size := uint32(1) << uint(bits-ones) // #nosec G115 -- bits-ones is in [0, 32] for IPv4
	return binary.BigEndian.Uint32(base) + size - 2, ipnet, nil
}

// allocateIP returns the first free host address in cidr, scanning .10 upward
// and skipping the addresses reserved for node ports.
func allocateIP(cidr string, used map[string]bool) (string, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("manager: invalid cidr %q: %w", cidr, err)
	}
	base := ipnet.IP.To4()
	if base == nil {
		return "", fmt.Errorf("manager: only IPv4 subnets are supported: %q", cidr)
	}
	for i := 10; i < 250; i++ {
		cand := net.IPv4(base[0], base[1], base[2], byte(i))
		if !ipnet.Contains(cand) {
			continue
		}
		if last, _, lErr := lastHost(cidr); lErr == nil && binary.BigEndian.Uint32(cand.To4()) > last-maxNodePorts {
			continue
		}
		if s := cand.String(); !used[s] {
			return s, nil
		}
	}
	return "", fmt.Errorf("manager: no free address in %s", cidr)
}

// prefixLen returns the prefix length of cidr, defaulting to 24.
func prefixLen(cidr string) int {
	if _, ipnet, err := net.ParseCIDR(cidr); err == nil {
		ones, _ := ipnet.Mask.Size()
		return ones
	}
	return 24
}

// firstHostOf returns the first usable host address of cidr (the VPC gateway).
func firstHostOf(cidr string) string {
	gw, err := gatewayCIDR(cidr)
	if err != nil {
		return ""
	}
	return hostOf(gw)
}

// teardownOf is what DeleteCompute needs to undo a creation of req.
func teardownOf(req ComputeRequest, rootfs string) ComputeTeardown {
	return ComputeTeardown{
		UID: req.UID, VPCID: req.VPCID, Bridge: req.Bridge, IP: req.IP,
		Ports: req.Ports, SGChain: req.SGChain, Rootfs: rootfs, Disks: req.Disks,
	}
}

// computeState tracks the applied request and the host launcher identity.
type computeState struct {
	Request   ComputeRequest
	PID       int
	StartTime string
}

func (b *ExecComputeBackend) loadComputeState(uid string) (computeState, error) {
	var snapshot computeState
	data, err := os.ReadFile(filepath.Join(b.stateDir, "compute", uid+".runtime.json")) // #nosec G304 -- agent-owned per-compute runtime state
	if err != nil {
		return snapshot, err
	}
	err = json.Unmarshal(data, &snapshot)
	return snapshot, err
}
func (b *ExecComputeBackend) saveComputeState(uid string, snapshot computeState) error {
	dir := filepath.Join(b.stateDir, "compute")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, uid+".runtime.json"), data, 0600)
}
func processStartTime(pid int) (string, error) {
	if pid <= 1 {
		return "", fmt.Errorf("invalid workload PID")
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)) // #nosec G304 -- numeric host PID under procfs
	if err != nil {
		return "", err
	}
	end := strings.LastIndex(string(data), ")")
	if end < 0 {
		return "", fmt.Errorf("invalid process stat")
	}
	fields := strings.Fields(string(data)[end+1:])
	if len(fields) < 20 || fields[0] == "Z" {
		return "", fmt.Errorf("workload is not running")
	}
	return fields[19], nil
}
func processMatches(pid int, expected string) bool {
	actual, err := processStartTime(pid)
	return err == nil && expected != "" && actual == expected
}
