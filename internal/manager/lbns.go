// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
)

// Every VPC gets, on every host, a network namespace of its own for its load
// balancers: the "load-balancer namespace". Its virtual services live there
// rather than in the host, so they keep working once the VPC's routes leave
// the host's main table, and two VPCs never share a virtual-service table.
// It has three legs:
//
//   - la0, the anycast port, on the VPC bridge with the same MAC on every host
//     (lbMAC): it holds the VIPs, so an instance's ARP for a VIP is answered by
//     its own host's namespace, the overlay dropping the other hosts' replies
//     like it does the gateway's (see overlay.go);
//   - np0, the node port, on the VPC bridge with a MAC of its own: it holds this
//     host's address in each subnet, which the namespace masquerades the
//     traffic it forwards to, so backends on any host reply to this one;
//   - pub0, the public leg, on an edge only: a routed veth to the host, which
//     sends the public addresses it serves there, while the namespace's default
//     route sends replies back through it.
//
// The agent runs in a private mount namespace (PrivateTmp= and friends), so a
// namespace it named under /run/netns would vanish with it on restart, taking
// the load balancers down. A systemd unit, infra-netns@<name>.service, holds
// the namespace instead (PrivateNetwork=yes): it outlives the agent, and the
// agent enters it through the unit's main process, /proc/<pid>/ns/net.

// The fixed names of the legs inside the namespace, and the public leg's
// link-local addresses: the namespace's default route goes through the host's.
const (
	lbAnycastIface = "la0"
	lbNodeIface    = "np0"
	lbPublicIface  = "pub0"
	lbPublicHost   = "169.254.0.1"
	lbPublicNS     = "169.254.0.2"
)

// lbNamespaceName names a VPC's load-balancer namespace, and lbNamespaceUnit
// the systemd unit holding it.
func lbNamespaceName(uid string) string { return ifaceName("lb-", uid) }
func lbNamespaceUnit(uid string) string { return "infra-netns@" + lbNamespaceName(uid) + ".service" }

// The host ends of the namespace's legs.
func lbAnycastPeerName(uid string) string { return ifaceName("la-", uid) }
func lbPublicPeerName(uid string) string  { return ifaceName("lp-", uid) }

// lbMAC derives the anycast port's MAC from the VPC's UID: locally
// administered and unicast, identical on every host, and distinct from the
// gateway's (gatewayMAC), which hashes the same UID without the suffix.
func lbMAC(uid string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(uid + "/lb"))
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], h.Sum64())
	return fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x", b[3], b[4], b[5], b[6], b[7])
}

// lbNamespaces finds and enters the load-balancer namespaces.
type lbNamespaces struct {
	run Runner
}

// pid returns the main PID of the unit holding uid's namespace, starting the
// unit when start is set and it is not running. Without start, a namespace
// that is not running yields 0.
func (n lbNamespaces) pid(ctx context.Context, uid string, start bool) (int, error) {
	unit := lbNamespaceUnit(uid)
	for attempt := 0; ; attempt++ {
		out, err := n.run(ctx, "systemctl", "show", "--property=MainPID", "--value", unit)
		if err != nil {
			return 0, fmt.Errorf("manager: show %s: %w: %s", unit, err, strings.TrimSpace(out))
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(out))
		if pid > 0 || !start {
			return pid, nil
		}
		if attempt > 0 {
			return 0, fmt.Errorf("manager: %s started without a main process", unit)
		}
		if out, err := n.run(ctx, "systemctl", "start", unit); err != nil {
			return 0, fmt.Errorf("manager: start %s: %w: %s", unit, err, strings.TrimSpace(out))
		}
	}
}

// in runs name with args inside the namespace held by pid.
func (n lbNamespaces) in(pid int) Runner {
	return func(ctx context.Context, name string, args ...string) (string, error) {
		return n.run(ctx, "nsenter", append([]string{"--net=/proc/" + strconv.Itoa(pid) + "/ns/net", "--", name}, args...)...)
	}
}

// stop stops the unit holding uid's namespace, which the kernel then frees
// with every leg in it. Stopping a unit that is not running is not an error.
func (n lbNamespaces) stop(ctx context.Context, uid string) error {
	unit := lbNamespaceUnit(uid)
	if out, err := n.run(ctx, "systemctl", "stop", unit); err != nil && !strings.Contains(out, "not loaded") {
		return fmt.Errorf("manager: stop %s: %w: %s", unit, err, strings.TrimSpace(out))
	}
	return nil
}

// linkExists reports whether the interface name is present where run looks.
func linkExists(ctx context.Context, run Runner, name string) (bool, error) {
	out, err := run(ctx, "ip", "link", "show", name)
	if err == nil {
		return true, nil
	}
	if strings.Contains(out, "does not exist") || strings.Contains(out, "Cannot find device") {
		return false, nil
	}
	return false, fmt.Errorf("manager: show link %q: %w: %s", name, err, strings.TrimSpace(out))
}

// ensureLeg creates the veth pair host <-> inner, the inner end inside the
// namespace held by pid, unless the host end already exists. The pair lives
// and dies with the namespace: when the holder restarts, the host end goes
// with the old namespace, and the next pass creates both again.
func ensureLeg(ctx context.Context, run Runner, pid int, host, inner string) error {
	exists, err := linkExists(ctx, run, host)
	if err != nil || exists {
		return err
	}
	mtu := strconv.Itoa(overlayMTU)
	if out, err := run(ctx, "ip", "link", "add", host, "mtu", mtu, "type", "veth",
		"peer", "name", inner, "mtu", mtu, "netns", strconv.Itoa(pid)); err != nil {
		return fmt.Errorf("manager: add leg %s/%s: %w: %s", host, inner, err, strings.TrimSpace(out))
	}
	return nil
}

// runSteps runs each command, stopping at the first failure.
func runSteps(ctx context.Context, run Runner, what string, steps [][]string) error {
	for _, st := range steps {
		if out, err := run(ctx, st[0], st[1:]...); err != nil {
			return fmt.Errorf("manager: %s %v: %w: %s", what, st, err, strings.TrimSpace(out))
		}
	}
	return nil
}

// deleteAddress removes addrCIDR from iface where run looks, ignoring an
// absent address or interface (see ExecBackend.DeleteAddress).
func deleteAddress(ctx context.Context, run Runner, iface, addrCIDR string) error {
	out, err := run(ctx, "ip", "-o", "addr", "show", "dev", iface)
	if err != nil {
		if strings.Contains(out, "does not exist") || strings.Contains(out, "Cannot find device") {
			return nil
		}
		return fmt.Errorf("manager: show addresses of %s: %w: %s", iface, err, strings.TrimSpace(out))
	}
	if !strings.Contains(out, " "+addrCIDR+" ") {
		return nil
	}
	if out, err := run(ctx, "ip", "addr", "del", addrCIDR, "dev", iface); err != nil {
		return fmt.Errorf("manager: del address %s on %s: %w: %s", addrCIDR, iface, err, strings.TrimSpace(out))
	}
	return nil
}

// ensureVIP puts vip on the namespace's anycast port, run entering it as in
// does, and routes it from the host onto the bridge in the VPC's table (see
// vrf.go), where only this host's namespace answers for it. The host
// forwards back out the interface the request came in on, which must not
// make it redirect the instance.
func ensureVIP(ctx context.Context, run, in Runner, vpcID, bridge, vip string) error {
	if err := runSteps(ctx, run, "vip route", [][]string{
		{"sysctl", "-w", "net.ipv4.conf." + bridge + ".send_redirects=0"},
		{"ip", "route", "replace", vip + "/32", "dev", bridge, "table", vrfTableArg(vpcID)},
	}); err != nil {
		return err
	}
	// replace, not add: idempotent without matching iproute2's error text
	// (see ExecBackend.EnsureAddress).
	if out, err := in(ctx, "ip", "addr", "replace", vip+"/32", "dev", lbAnycastIface); err != nil {
		return fmt.Errorf("manager: add vip %s: %w: %s", vip, err, strings.TrimSpace(out))
	}
	return nil
}

// ensureHostLeg plugs the namespace held by pid, entered as in does, into
// the host with a routed veth: the host answers the namespace's default
// gateway on it, and what the namespace sends out that is not for the VPC
// goes through the host's main table.
func ensureHostLeg(ctx context.Context, run, in Runner, pid int, vpcID string) error {
	host := lbPublicPeerName(vpcID)
	if err := ensureLeg(ctx, run, pid, host, lbPublicIface); err != nil {
		return err
	}
	if err := runSteps(ctx, run, "host leg", [][]string{
		{"ip", "addr", "replace", lbPublicHost + "/32", "dev", host},
		{"ip", "link", "set", host, "up"},
	}); err != nil {
		return err
	}
	return runSteps(ctx, in, "host leg", [][]string{
		{"ip", "addr", "replace", lbPublicNS + "/32", "dev", lbPublicIface},
		{"ip", "link", "set", lbPublicIface, "up"},
		{"sysctl", "-w", "net.ipv4.conf." + lbPublicIface + ".rp_filter=0"},
		{"ip", "route", "replace", "default", "via", lbPublicHost, "dev", lbPublicIface, "onlink"},
	})
}
