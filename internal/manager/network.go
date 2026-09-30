// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package manager reconciles declarative resources against Linux kernel
// primitives. Kernel access sits behind interfaces so the reconciliation logic
// is testable without root; the real backends shell out to iproute2 and only
// run on a privileged host.
package manager

import (
	"context"
	"fmt"
	"hash/fnv"
	"os/exec"
	"strconv"
	"strings"
)

// maxIfaceName is the Linux interface name length limit (IFNAMSIZ - 1).
const maxIfaceName = 15

// Bridge describes the desired state of a Linux bridge.
type Bridge struct {
	// Name is the kernel interface name (<= 15 chars).
	Name string
	// CIDR is the VPC address range the bridge fabricates (informational at the
	// bridge level; subnets assign addresses).
	CIDR string
}

// NetworkBackend abstracts the kernel network operations the agent needs.
type NetworkBackend interface {
	// EnsureBridge creates the bridge if absent and brings it up. Idempotent.
	EnsureBridge(ctx context.Context, b Bridge) error
	// DeleteBridge removes the bridge. Deleting an absent bridge is not an error.
	DeleteBridge(ctx context.Context, name string) error
	// BridgeExists reports whether the named bridge is present.
	BridgeExists(ctx context.Context, name string) (bool, error)
	// EnsureAddress assigns addrCIDR to iface if not already present. Idempotent.
	EnsureAddress(ctx context.Context, iface, addrCIDR string) error
	// DeleteAddress removes addrCIDR from iface. Removing an absent address is
	// not an error.
	DeleteAddress(ctx context.Context, iface, addrCIDR string) error
	// EnableForwarding turns on IPv4 forwarding.
	EnableForwarding(ctx context.Context) error
	// EnsureNAT adds a masquerade rule for sourceCIDR egressing via hostIface.
	// Idempotent.
	EnsureNAT(ctx context.Context, sourceCIDR, hostIface string) error
	// DeleteNAT removes the masquerade rule. Removing an absent rule is not an
	// error.
	DeleteNAT(ctx context.Context, sourceCIDR, hostIface string) error
	// DefaultInterface returns the host's default-route interface.
	DefaultInterface(ctx context.Context) (string, error)
	// EnsureNodePort gives the host a port of its own on the VPC bridge: a
	// veth pair whose peer end is enslaved to bridge and whose port end, with
	// a MAC of its own, carries this host's address in each subnet. Idempotent.
	EnsureNodePort(ctx context.Context, bridge, port, peer string) error
	// DeleteNodePort removes the pair. Removing an absent port is not an error.
	DeleteNodePort(ctx context.Context, port string) error
	// EnsureGatewayAddress assigns a subnet gateway to the bridge without the
	// subnet's connected route, which the node port carries instead. An
	// existing address lacking that flag is re-added with it. Idempotent.
	EnsureGatewayAddress(ctx context.Context, bridge, addrCIDR string) error
}

// The subnet gateways sit on the VPC bridge, which takes the same anycast MAC
// on every host (see overlay.go). Traffic the host itself sends into the VPC -
// to an instance on another host, or on behalf of a load balancer - must not
// leave with that MAC: the reply would be taken by the receiving host's own
// bridge. It leaves through the node port instead, a veth whose host end has
// a MAC of its own and one address per subnet unique to this host, and which
// carries the subnets' connected routes. arp_ignore=1 on both keeps each one
// answering ARP only for the addresses it holds.

// nodePortName and nodePortPeerName derive the two ends of a VPC's node port.
func nodePortName(uid string) string     { return ifaceName("np-", uid) }
func nodePortPeerName(uid string) string { return ifaceName("nb-", uid) }

// bridgeName derives a valid, deterministic bridge interface name from a UID,
// hashing when a sanitized "br-<uid>" would exceed the kernel length limit.
func bridgeName(uid string) string {
	return ifaceName("br-", uid)
}

// ifaceName derives a valid, deterministic interface name from a prefix and a
// UID, hashing when the sanitized "<prefix><uid>" would exceed the kernel
// length limit.
func ifaceName(prefix, uid string) string {
	var b strings.Builder
	for _, r := range uid {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		}
	}
	name := prefix + b.String()
	if len(name) <= maxIfaceName {
		return name
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(uid))
	return fmt.Sprintf("%s%08x", prefix, h.Sum32())
}

// Runner runs an external command and returns its combined output.
type Runner func(ctx context.Context, name string, args ...string) (string, error)

func defaultRun(ctx context.Context, name string, args ...string) (string, error) {
	// #nosec G204 -- the agent intentionally shells out to iproute2 with a
	// fixed command name and arguments it controls, not user input.
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

// ExecBackend is a NetworkBackend that shells out to iproute2 (`ip`). It
// requires root / CAP_NET_ADMIN at run time.
type ExecBackend struct {
	run Runner
}

// NewExecBackend returns an ExecBackend using the real `ip` command.
func NewExecBackend() *ExecBackend {
	return &ExecBackend{run: defaultRun}
}

// NewExecBackendWithRunner returns an ExecBackend driven by a custom runner,
// used in tests to assert the issued commands without touching the kernel.
func NewExecBackendWithRunner(run Runner) *ExecBackend {
	return &ExecBackend{run: run}
}

// BridgeExists reports whether `ip link show <name>` finds the bridge.
func (b *ExecBackend) BridgeExists(ctx context.Context, name string) (bool, error) {
	out, err := b.run(ctx, "ip", "link", "show", name)
	if err == nil {
		return true, nil
	}
	if strings.Contains(out, "does not exist") || strings.Contains(out, "Cannot find device") {
		return false, nil
	}
	return false, fmt.Errorf("manager: bridge exists %q: %w: %s", name, err, strings.TrimSpace(out))
}

// EnsureBridge creates the bridge if needed and brings it up.
func (b *ExecBackend) EnsureBridge(ctx context.Context, br Bridge) error {
	exists, err := b.BridgeExists(ctx, br.Name)
	if err != nil {
		return err
	}
	if !exists {
		if out, err := b.run(ctx, "ip", "link", "add", "name", br.Name, "type", "bridge"); err != nil {
			return fmt.Errorf("manager: add bridge %q: %w: %s", br.Name, err, strings.TrimSpace(out))
		}
	}
	if out, err := b.run(ctx, "ip", "link", "set", br.Name, "up"); err != nil {
		return fmt.Errorf("manager: set bridge %q up: %w: %s", br.Name, err, strings.TrimSpace(out))
	}
	return nil
}

// DeleteBridge removes the bridge if present.
func (b *ExecBackend) DeleteBridge(ctx context.Context, name string) error {
	exists, err := b.BridgeExists(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if out, err := b.run(ctx, "ip", "link", "del", name); err != nil {
		return fmt.Errorf("manager: delete bridge %q: %w: %s", name, err, strings.TrimSpace(out))
	}
	return nil
}

// EnsureAddress assigns addrCIDR to iface, treating an existing address as ok.
func (b *ExecBackend) EnsureAddress(ctx context.Context, iface, addrCIDR string) error {
	out, err := b.run(ctx, "ip", "addr", "add", addrCIDR, "dev", iface)
	if err != nil && !strings.Contains(out, "File exists") {
		return fmt.Errorf("manager: add address %s on %s: %w: %s", addrCIDR, iface, err, strings.TrimSpace(out))
	}
	return nil
}

// DeleteAddress removes addrCIDR from iface, ignoring an absent address.
//
// Whether the address is there is read first rather than inferred from the
// error of `ip addr del`, whose wording for an absent address changed across
// kernels ("Cannot assign requested address", then "Address not found").
func (b *ExecBackend) DeleteAddress(ctx context.Context, iface, addrCIDR string) error {
	out, err := b.run(ctx, "ip", "-o", "addr", "show", "dev", iface)
	if err != nil {
		if strings.Contains(out, "does not exist") || strings.Contains(out, "Cannot find device") {
			return nil
		}
		return fmt.Errorf("manager: show addresses of %s: %w: %s", iface, err, strings.TrimSpace(out))
	}
	if !strings.Contains(out, " "+addrCIDR+" ") {
		return nil
	}
	if out, err := b.run(ctx, "ip", "addr", "del", addrCIDR, "dev", iface); err != nil {
		return fmt.Errorf("manager: del address %s on %s: %w: %s", addrCIDR, iface, err, strings.TrimSpace(out))
	}
	return nil
}

// EnableForwarding turns on IPv4 forwarding.
func (b *ExecBackend) EnableForwarding(ctx context.Context) error {
	if out, err := b.run(ctx, "sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return fmt.Errorf("manager: enable forwarding: %w: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// EnsureNAT adds a masquerade rule for sourceCIDR via hostIface if absent.
func (b *ExecBackend) EnsureNAT(ctx context.Context, sourceCIDR, hostIface string) error {
	if _, err := b.run(ctx, "iptables", "-t", "nat", "-C", "POSTROUTING", "-s", sourceCIDR, "-o", hostIface, "-j", "MASQUERADE"); err == nil {
		return nil
	}
	if out, err := b.run(ctx, "iptables", "-t", "nat", "-A", "POSTROUTING", "-s", sourceCIDR, "-o", hostIface, "-j", "MASQUERADE"); err != nil {
		return fmt.Errorf("manager: add nat for %s via %s: %w: %s", sourceCIDR, hostIface, err, strings.TrimSpace(out))
	}
	return nil
}

// DeleteNAT removes the masquerade rule (best effort).
func (b *ExecBackend) DeleteNAT(ctx context.Context, sourceCIDR, hostIface string) error {
	_, _ = b.run(ctx, "iptables", "-t", "nat", "-D", "POSTROUTING", "-s", sourceCIDR, "-o", hostIface, "-j", "MASQUERADE")
	return nil
}

// DefaultInterface returns the host's default-route interface.
func (b *ExecBackend) DefaultInterface(ctx context.Context) (string, error) {
	out, err := b.run(ctx, "ip", "route", "show", "default")
	if err != nil {
		return "", fmt.Errorf("manager: default route: %w: %s", err, strings.TrimSpace(out))
	}
	fields := strings.Fields(out)
	for i, f := range fields {
		if f == "dev" && i+1 < len(fields) {
			return fields[i+1], nil
		}
	}
	return "", fmt.Errorf("manager: no default-route interface found")
}

// EnsureNodePort creates, attaches and brings up the node port.
func (b *ExecBackend) EnsureNodePort(ctx context.Context, bridge, port, peer string) error {
	exists, err := b.BridgeExists(ctx, port)
	if err != nil {
		return err
	}
	mtu := strconv.Itoa(overlayMTU)
	steps := [][]string{}
	if !exists {
		steps = append(steps, []string{"ip", "link", "add", port, "mtu", mtu, "type", "veth", "peer", "name", peer, "mtu", mtu})
	}
	steps = append(steps,
		[]string{"ip", "link", "set", peer, "master", bridge},
		[]string{"ip", "link", "set", peer, "up"},
		[]string{"ip", "link", "set", port, "up"},
		[]string{"sysctl", "-w", "net.ipv4.conf." + bridge + ".arp_ignore=1"},
		[]string{"sysctl", "-w", "net.ipv4.conf." + port + ".arp_ignore=1"},
	)
	for _, st := range steps {
		if out, err := b.run(ctx, st[0], st[1:]...); err != nil {
			return fmt.Errorf("manager: node port %q %v: %w: %s", port, st, err, strings.TrimSpace(out))
		}
	}
	return nil
}

// DeleteNodePort removes the node port's veth pair.
func (b *ExecBackend) DeleteNodePort(ctx context.Context, port string) error {
	out, err := b.run(ctx, "ip", "link", "del", port)
	if err != nil && !strings.Contains(out, "Cannot find device") && !strings.Contains(out, "does not exist") {
		return fmt.Errorf("manager: delete node port %q: %w: %s", port, err, strings.TrimSpace(out))
	}
	return nil
}

// EnsureGatewayAddress assigns addrCIDR to bridge with noprefixroute. `ip addr
// replace` does not update the flags of an address already present, so one
// assigned before the node port existed is deleted and added again.
func (b *ExecBackend) EnsureGatewayAddress(ctx context.Context, bridge, addrCIDR string) error {
	out, err := b.run(ctx, "ip", "-o", "-4", "addr", "show", "dev", bridge)
	if err != nil {
		return fmt.Errorf("manager: show addresses of %q: %w: %s", bridge, err, strings.TrimSpace(out))
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, " inet "+addrCIDR+" ") {
			continue
		}
		if strings.Contains(line, " noprefixroute ") {
			return nil
		}
		if err := b.DeleteAddress(ctx, bridge, addrCIDR); err != nil {
			return err
		}
	}
	if out, err := b.run(ctx, "ip", "addr", "add", addrCIDR, "dev", bridge, "noprefixroute"); err != nil {
		return fmt.Errorf("manager: add gateway %s on %s: %w: %s", addrCIDR, bridge, err, strings.TrimSpace(out))
	}
	return nil
}
