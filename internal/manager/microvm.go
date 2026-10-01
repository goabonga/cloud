// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// chvShutdownGrace bounds how long DeleteMicroVM waits for a graceful
// vmm.shutdown before killing the process.
const chvShutdownGrace = time.Second

// MicroVMRequest is a fully resolved micro-VM to realize on the host: the
// reconciler has already resolved the bridge, an address and the subnet
// gateway.
type MicroVMRequest struct {
	UID        string
	Hostname   string
	VCPUs      int
	MemoryMB   int
	KernelPath string
	InitrdPath string
	CmdLine    string
	Image      string
	// SSHAuthorizedKey and UserData seed cloud-init: UserData, when set, is
	// used verbatim as the guest's user-data; otherwise a minimal
	// cloud-config carries the hostname and the SSH key.
	SSHAuthorizedKey string
	UserData         string
	Bridge           string
	IP               string
	Prefix           int
	Gateway          string
	SGChain          string
}

// MicroVMResult reports the realized topology of a micro-VM.
type MicroVMResult struct {
	Tap string
	Pid int
}

// MicroVMTeardown is the information needed to tear an instance down without
// its live spec, reconstructed from status and spec by the reconciler.
type MicroVMTeardown struct {
	UID     string
	Tap     string
	IP      string
	SGChain string
}

// MicroVMBackend abstracts the host operations a micro-VM needs.
type MicroVMBackend interface {
	// EnsureMicroVM realizes req and returns its topology. It boots the VM
	// once: a subsequent call for a live cloud-hypervisor process is a no-op.
	EnsureMicroVM(ctx context.Context, req MicroVMRequest) (MicroVMResult, error)
	// DeleteMicroVM tears an instance down. Tearing down an absent instance is
	// not an error.
	DeleteMicroVM(ctx context.Context, td MicroVMTeardown) error
}

// ExecMicroVMBackend realizes micro-VMs as cloud-hypervisor processes attached
// to a TAP device on the VPC bridge. It requires root / CAP_NET_ADMIN and the
// cloud-hypervisor binary on PATH.
type ExecMicroVMBackend struct {
	run      Runner
	net      *ExecBackend
	images   *vmImageCache
	seed     *cloudInitSeeds
	stateDir string
	binary   string
}

// NewExecMicroVMBackend stores per-instance state (api socket, pid, log,
// boot disk) and the shared image cache under stateDir, and resolves
// cloud-hypervisor through PATH.
func NewExecMicroVMBackend(stateDir string) *ExecMicroVMBackend {
	return &ExecMicroVMBackend{run: defaultRun, net: NewExecBackendWithRunner(defaultRun), images: newVMImageCache(stateDir), seed: newCloudInitSeeds(cloudInitPort), stateDir: stateDir}
}

// NewExecMicroVMBackendWithRunner builds a backend with an overridable
// directory and runner, used in tests to assert the issued commands without
// touching the host or starting cloud-hypervisor. Its cloud-init seed server,
// if started, binds an OS-assigned port rather than the production one, so
// parallel tests never collide on it.
func NewExecMicroVMBackendWithRunner(stateDir string, run Runner) *ExecMicroVMBackend {
	return &ExecMicroVMBackend{run: run, net: NewExecBackendWithRunner(run), images: newVMImageCache(stateDir), seed: newCloudInitSeeds(0), stateDir: stateDir}
}

func (b *ExecMicroVMBackend) instanceDir(uid string) string {
	return filepath.Join(b.stateDir, "microvm", uid)
}

// EnsureMicroVM creates the TAP device, attaches it to the bridge, applies the
// security-group chain and starts cloud-hypervisor the first time; a live
// process from a previous call is left alone.
func (b *ExecMicroVMBackend) EnsureMicroVM(ctx context.Context, req MicroVMRequest) (MicroVMResult, error) {
	tap := ifaceName("tap-", req.UID)
	res := MicroVMResult{Tap: tap}
	dir := b.instanceDir(req.UID)
	pidFile := filepath.Join(dir, "chv.pid")

	if pid, alive := readAlivePid(pidFile); alive {
		res.Pid = pid
		return res, nil
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return res, fmt.Errorf("manager: microvm state dir %q: %w", dir, err)
	}

	exists, err := b.net.BridgeExists(ctx, tap)
	if err != nil {
		return res, err
	}
	if !exists {
		if out, err := b.run(ctx, "ip", "tuntap", "add", "dev", tap, "mode", "tap"); err != nil {
			return res, fmt.Errorf("manager: create tap %q: %w: %s", tap, err, strings.TrimSpace(out))
		}
	}
	if out, err := b.run(ctx, "ip", "link", "set", tap, "master", req.Bridge); err != nil {
		return res, fmt.Errorf("manager: attach tap %q to %q: %w: %s", tap, req.Bridge, err, strings.TrimSpace(out))
	}
	if out, err := b.run(ctx, "ip", "link", "set", tap, "up"); err != nil {
		return res, fmt.Errorf("manager: bring tap %q up: %w: %s", tap, err, strings.TrimSpace(out))
	}

	if req.SGChain != "" {
		if err := b.iptablesEnsure(ctx, []string{"-A", "FORWARD", "-d", req.IP, "-j", req.SGChain}); err != nil {
			return res, err
		}
		if err := b.iptablesEnsure(ctx, []string{"-A", "OUTPUT", "-d", req.IP, "-j", req.SGChain}); err != nil {
			return res, err
		}
	}

	disk, err := b.images.resolve(ctx, req.UID, req.Image)
	if err != nil {
		return res, fmt.Errorf("manager: resolve boot image for %q: %w", req.UID, err)
	}

	if err := b.seed.ensureStarted(); err != nil {
		return res, err
	}
	b.seed.set(req.UID, buildCloudInitSeed(req))

	sock := filepath.Join(dir, "api.sock")
	handle, err := startVMM(ctx, b.binary, sock, filepath.Join(dir, "chv.log"))
	if err != nil {
		return res, err
	}
	res.Pid = handle.pid

	client := newVMMClient(handle.sock)
	cfg := chVMConfig{
		CPUs:    chCPUsConfig{BootVCPUs: req.VCPUs, MaxVCPUs: req.VCPUs},
		Memory:  chMemoryConfig{SizeBytes: int64(req.MemoryMB) * 1024 * 1024},
		Payload: chPayloadConfig{Kernel: req.KernelPath, Initramfs: req.InitrdPath, Cmdline: guestCmdline(req)},
		Disks:   []chDiskConfig{{Path: disk}},
		Net:     []chNetConfig{{Tap: tap}},
		Serial:  chConsoleConfig{Mode: "Tty"},
		Console: chConsoleConfig{Mode: "Off"},
	}
	if err := client.createVM(ctx, cfg); err != nil {
		_ = syscall.Kill(handle.pid, syscall.SIGKILL)
		return res, err
	}
	if err := client.bootVM(ctx); err != nil {
		_ = syscall.Kill(handle.pid, syscall.SIGKILL)
		return res, err
	}
	if err := writePidFile(pidFile, handle.pid); err != nil {
		return res, err
	}
	return res, nil
}

// DeleteMicroVM shuts the cloud-hypervisor process down (killing it if it
// doesn't respond), removes the TAP device and the security-group rules.
// Best effort: tearing down an absent instance is not an error.
func (b *ExecMicroVMBackend) DeleteMicroVM(ctx context.Context, td MicroVMTeardown) error {
	dir := b.instanceDir(td.UID)
	pidFile := filepath.Join(dir, "chv.pid")
	b.seed.remove(td.UID)

	if pid, alive := readAlivePid(pidFile); alive {
		shutdownCtx, cancel := context.WithTimeout(ctx, chvShutdownGrace)
		_ = newVMMClient(filepath.Join(dir, "api.sock")).shutdownVMM(shutdownCtx)
		cancel()
		deadline := time.Now().Add(chvShutdownGrace)
		for processAlive(pid) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if processAlive(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}

	tap := td.Tap
	if tap == "" {
		tap = ifaceName("tap-", td.UID)
	}
	_ = b.net.DeleteBridge(ctx, tap) // `ip link del` removes TAP devices too

	if td.SGChain != "" && td.IP != "" {
		_, _ = b.run(ctx, "iptables", "-D", "FORWARD", "-d", td.IP, "-j", td.SGChain)
		_, _ = b.run(ctx, "iptables", "-D", "OUTPUT", "-d", td.IP, "-j", td.SGChain)
	}

	_ = os.RemoveAll(dir)
	return nil
}

// iptablesEnsure adds an iptables rule unless an identical one already exists.
func (b *ExecMicroVMBackend) iptablesEnsure(ctx context.Context, args []string) error {
	check := make([]string, len(args))
	copy(check, args)
	for i, a := range check {
		if a == "-A" {
			check[i] = "-C"
			break
		}
	}
	if _, err := b.run(ctx, "iptables", check...); err == nil {
		return nil
	}
	if out, err := b.run(ctx, "iptables", args...); err != nil {
		return fmt.Errorf("manager: iptables %v: %w: %s", args, err, strings.TrimSpace(out))
	}
	return nil
}

// readAlivePid reads a pid bookkeeping file and reports it only if the
// process it names is still alive, so a stale file from a killed instance is
// treated the same as no file at all.
func readAlivePid(path string) (int, bool) {
	data, err := os.ReadFile(path) // #nosec G304 -- agent-owned state path
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || !processAlive(pid) {
		return 0, false
	}
	return pid, true
}

// writePidFile records a cloud-hypervisor process's pid for later liveness
// checks and teardown.
func writePidFile(path string, pid int) error {
	// #nosec G306 -- state bookkeeping file, not sensitive
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return fmt.Errorf("manager: write pid file %q: %w", path, err)
	}
	return nil
}

// guestCmdline appends a kernel "ip=" directive derived from the allocated
// address so the guest configures eth0 at boot without cloud-init (not
// implemented yet).
func guestCmdline(req MicroVMRequest) string {
	parts := []string{}
	if req.CmdLine != "" {
		parts = append(parts, req.CmdLine)
	}
	if req.IP != "" && req.Gateway != "" {
		mask := net.IP(net.CIDRMask(req.Prefix, 32)).String()
		parts = append(parts, fmt.Sprintf("ip=%s::%s:%s::eth0:off", req.IP, req.Gateway, mask))
	}
	if req.Gateway != "" {
		parts = append(parts, cloudInitCmdline(req))
	}
	return strings.Join(parts, " ")
}

// MicroVMRegistry is the typed store the micro-VM reconciler reads and writes.
type MicroVMRegistry = registry.Registry[resource.MicroVMSpec, resource.MicroVMStatus]

// MicroVMReconciler realizes a micro-VM: it resolves the subnet, VPC bridge,
// an address and the security-group chain, then asks the backend to boot
// cloud-hypervisor.
type MicroVMReconciler struct {
	reg     *MicroVMRegistry
	subnets *SubnetRegistry
	vpcs    *VPCRegistry
	sgs     *SecurityGroupRegistry
	backend MicroVMBackend
	// nodeName scopes realization to micro-VMs scheduled to this node. When
	// empty the reconciler realizes every micro-VM (single-host mode); there
	// is no scheduler support for microvm yet, so status.nodeName never gets
	// set regardless.
	nodeName string
	// addresses reserves instance addresses in the shared store; nil falls
	// back to picking one no other instance lists, which is only safe with a
	// single agent (mirrors ComputeReconciler.addresses).
	addresses *addressBook
}

// NewMicroVMReconciler returns a reconciler backed by the resource stores and
// the micro-VM backend.
func NewMicroVMReconciler(reg *MicroVMRegistry, subnets *SubnetRegistry, vpcs *VPCRegistry, sgs *SecurityGroupRegistry, backend MicroVMBackend, nodeName string) *MicroVMReconciler {
	return &MicroVMReconciler{reg: reg, subnets: subnets, vpcs: vpcs, sgs: sgs, backend: backend, nodeName: nodeName}
}

// WithAddressStore makes the reconciler reserve each instance address in
// store before using it, so agents allocating at once on different hosts
// never hand out the same one.
func (r *MicroVMReconciler) WithAddressStore(store state.Store) *MicroVMReconciler {
	r.addresses = &addressBook{store: store}
	return r
}

// Name identifies the reconcile pass.
func (r *MicroVMReconciler) Name() string { return resource.KindMicroVM }

// ReconcileAll reconciles every micro-VM, collecting per-instance errors.
func (r *MicroVMReconciler) ReconcileAll(ctx context.Context) error {
	vms, err := r.reg.List()
	if err != nil {
		return fmt.Errorf("manager: list microvms: %w", err)
	}
	var errs []error
	for i := range vms {
		uid := vms[i].Metadata.UID
		if err := r.Reconcile(ctx, uid); err != nil {
			errs = append(errs, fmt.Errorf("microvm %s: %w", uid, err))
		}
	}
	return errors.Join(errs...)
}

// Reconcile brings the micro-VM identified by uid in line with its spec.
func (r *MicroVMReconciler) Reconcile(ctx context.Context, uid string) error {
	v, err := r.reg.Get(uid)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("manager: load microvm %q: %w", uid, err)
	}
	if r.nodeName != "" && v.Status.NodeName != r.nodeName {
		// Scheduled to another node (or not yet scheduled); leave it alone.
		return nil
	}
	if v.Metadata.IsDeleting() {
		return r.finalize(ctx, v)
	}
	return r.ensure(ctx, v)
}

func (r *MicroVMReconciler) ensure(ctx context.Context, v *resource.MicroVM) error {
	if !v.Metadata.HasFinalizer(resource.MicroVMFinalizer) {
		v.Metadata.AddFinalizer(resource.MicroVMFinalizer)
	}

	req, ready, err := r.resolve(v)
	if err != nil {
		v.Status.SetPhase(resource.PhaseError, "ResolveError", err.Error())
		_ = r.reg.Put(v)
		return err
	}
	if !ready {
		// A dependency is not provisioned yet; retry on the next pass.
		return r.reg.Put(v)
	}

	// Record the address before realising the instance, so a failed attempt
	// retries with the one it already holds instead of reserving another.
	v.Status.IP = req.IP
	v.Status.SetPhase(resource.PhaseReconciling, "Reconciling", "starting micro-VM")
	res, err := r.backend.EnsureMicroVM(ctx, req)
	if err != nil {
		v.Status.SetPhase(resource.PhaseError, "MicroVMError", err.Error())
		_ = r.reg.Put(v)
		return err
	}

	v.Status.IP = req.IP
	v.Status.Tap = res.Tap
	v.Status.Pid = res.Pid
	v.Status.Ready = true
	v.Status.MarkReconciled(v.Metadata.Generation)
	v.Status.SetPhase(resource.PhaseReady, "Running", "micro-VM ready")
	if err := r.reg.Put(v); err != nil {
		return fmt.Errorf("manager: save microvm %q: %w", v.Metadata.UID, err)
	}
	return nil
}

// resolve gathers the realized request for a micro-VM. ready is false when a
// dependency (subnet gateway, VPC bridge, security group) is not ready,
// signalling the caller to requeue without an error.
func (r *MicroVMReconciler) resolve(v *resource.MicroVM) (MicroVMRequest, bool, error) {
	subnet, err := r.subnets.Get(v.Spec.SubnetID)
	if errors.Is(err, state.ErrNotFound) {
		return MicroVMRequest{}, false, fmt.Errorf("subnet %q not found", v.Spec.SubnetID)
	}
	if err != nil {
		return MicroVMRequest{}, false, err
	}
	if subnet.Status.Gateway == "" {
		v.Status.SetPhase(resource.PhasePending, "WaitingForSubnet", "subnet gateway not ready")
		return MicroVMRequest{}, false, nil
	}

	vpc, err := r.vpcs.Get(subnet.Spec.VPCID)
	if errors.Is(err, state.ErrNotFound) {
		return MicroVMRequest{}, false, fmt.Errorf("vpc %q not found", subnet.Spec.VPCID)
	}
	if err != nil {
		return MicroVMRequest{}, false, err
	}
	if vpc.Status.BridgeName == "" {
		v.Status.SetPhase(resource.PhasePending, "WaitingForVPC", "vpc bridge not ready")
		return MicroVMRequest{}, false, nil
	}

	sgChain := ""
	if v.Spec.SecurityGroupID != "" {
		sg, sgErr := r.sgs.Get(v.Spec.SecurityGroupID)
		if errors.Is(sgErr, state.ErrNotFound) {
			return MicroVMRequest{}, false, fmt.Errorf("security group %q not found", v.Spec.SecurityGroupID)
		}
		if sgErr != nil {
			return MicroVMRequest{}, false, sgErr
		}
		if sg.Status.Chain == "" {
			v.Status.SetPhase(resource.PhasePending, "WaitingForSG", "security-group chain not ready")
			return MicroVMRequest{}, false, nil
		}
		sgChain = sg.Status.Chain
	}

	ip := v.Status.IP
	switch {
	case ip == "":
		used, uErr := r.usedIPs(v.Metadata.UID)
		if uErr != nil {
			return MicroVMRequest{}, false, uErr
		}
		used[subnet.Status.Gateway] = true
		if r.addresses != nil {
			ip, err = r.addresses.allocate(subnet.Spec.CIDR, v.Spec.SubnetID, v.Metadata.UID, used)
		} else {
			ip, err = allocateIP(subnet.Spec.CIDR, used)
		}
		if err != nil {
			return MicroVMRequest{}, false, err
		}
	case r.addresses != nil:
		// An address given before reservations existed, or kept across a
		// restart: hold it, unless another instance already does.
		ok, owner, cErr := r.addresses.claim(v.Spec.SubnetID, ip, v.Metadata.UID)
		if cErr != nil {
			return MicroVMRequest{}, false, cErr
		}
		if !ok {
			return MicroVMRequest{}, false, fmt.Errorf("address %s is reserved by %s; recreate this instance to get another", ip, owner)
		}
	}

	return MicroVMRequest{
		UID:              v.Metadata.UID,
		Hostname:         v.Spec.Hostname,
		VCPUs:            v.Spec.VCPUs,
		MemoryMB:         v.Spec.MemoryMB,
		KernelPath:       v.Spec.KernelPath,
		InitrdPath:       v.Spec.InitrdPath,
		CmdLine:          v.Spec.CmdLine,
		Image:            v.Spec.Image,
		SSHAuthorizedKey: v.Spec.SSHAuthorizedKey,
		UserData:         v.Spec.UserData,
		Bridge:           vpc.Status.BridgeName,
		IP:               ip,
		Prefix:           prefixLen(subnet.Spec.CIDR),
		Gateway:          subnet.Status.Gateway,
		SGChain:          sgChain,
	}, true, nil
}

func (r *MicroVMReconciler) finalize(ctx context.Context, v *resource.MicroVM) error {
	if v.Metadata.HasFinalizer(resource.MicroVMFinalizer) {
		td := MicroVMTeardown{
			UID:     v.Metadata.UID,
			Tap:     v.Status.Tap,
			IP:      v.Status.IP,
			SGChain: r.sgChain(v.Spec.SecurityGroupID),
		}
		if err := r.backend.DeleteMicroVM(ctx, td); err != nil {
			v.Status.SetPhase(resource.PhaseError, "MicroVMError", err.Error())
			_ = r.reg.Put(v)
			return err
		}
		if r.addresses != nil && v.Status.IP != "" {
			if err := r.addresses.release(v.Spec.SubnetID, v.Status.IP, v.Metadata.UID); err != nil {
				return err
			}
		}
		v.Metadata.RemoveFinalizer(resource.MicroVMFinalizer)
		v.Status.SetPhase(resource.PhaseDeleting, "Deleting", "micro-VM removed")
		if err := r.reg.Put(v); err != nil {
			return fmt.Errorf("manager: save microvm %q: %w", v.Metadata.UID, err)
		}
	}
	if len(v.Metadata.Finalizers) == 0 {
		if err := r.reg.Delete(v.Metadata.UID); err != nil {
			return fmt.Errorf("manager: delete microvm %q: %w", v.Metadata.UID, err)
		}
	}
	return nil
}

// sgChain returns the iptables chain of a security group, empty if it cannot
// be resolved (best effort, used during teardown).
func (r *MicroVMReconciler) sgChain(sgID string) string {
	if sgID == "" {
		return ""
	}
	if sg, err := r.sgs.Get(sgID); err == nil {
		return sg.Status.Chain
	}
	return ""
}

// usedIPs returns the addresses already assigned to other micro-VMs.
func (r *MicroVMReconciler) usedIPs(exclude string) (map[string]bool, error) {
	list, err := r.reg.List()
	if err != nil {
		return nil, fmt.Errorf("manager: list microvms: %w", err)
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
