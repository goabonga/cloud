// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"
	"strings"
)

// Every agent builds each VPC on a bridge of its own, so without an overlay two
// instances of the same subnet on different hosts sit on disjoint L2 segments.
// The overlay joins them: one VXLAN device per VPC, enslaved to the VPC bridge,
// flooding unknown and broadcast frames to every other node (head-end
// replication) and learning remote MACs from the traffic it receives.
//
// Each host also carries the subnet gateways on its bridge. Giving the bridge
// the same MAC on every host, derived from the VPC, turns those into an
// anycast gateway: whichever host answers an ARP request, the reply names a
// MAC every bridge owns, so an instance always routes through its own host.
// Frames sourced from that MAC are kept off the overlay: a reply to them would
// be taken by the receiving host's own bridge anyway, and every host would log
// the others' copies as its own address arriving from outside. The gateway is
// strictly local to each host as a result, and a host cannot reach an
// instance on another host directly. The load balancer VIPs work the same
// way, on the anycast port of each host's load-balancer namespace.
const (
	// vxlanPort is the IANA VXLAN port.
	vxlanPort = 4789
	// overlayMTU leaves room for the 50 bytes of VXLAN, UDP and outer IP
	// headers on a 1500-byte underlay. The VXLAN device and every compute veth
	// use it, so a frame an instance sends always fits once encapsulated.
	overlayMTU = 1450
	// floodMAC is the all-zeros FDB entry the kernel uses for unknown unicast,
	// broadcast and multicast frames.
	floodMAC = "00:00:00:00:00:00"
)

// VXLAN describes the desired state of a VPC's overlay device.
type VXLAN struct {
	// Name is the kernel interface name (<= 15 chars).
	Name string
	// VNI is the 24-bit VXLAN network identifier; it must match on every host.
	VNI uint32
	// Local is this host's underlay address, the source of the tunnel.
	Local string
	// Bridge is the VPC bridge the device is enslaved to.
	Bridge string
	// GatewayMAC is the bridge's anycast MAC; frames it sources are dropped
	// on the way into the overlay.
	GatewayMAC string
	// LBMAC is the anycast MAC of the VPC's load-balancer namespace (see
	// lbns.go), kept off the overlay the same way.
	LBMAC string
}

// OverlayBackend abstracts the kernel operations of the VPC overlay.
type OverlayBackend interface {
	// EnsureVXLAN creates the device if absent (or recreates it when its VNI or
	// local address changed), enslaves it to the bridge, keeps frames from the
	// gateway and load-balancer anycast MACs off it and brings it up.
	EnsureVXLAN(ctx context.Context, v VXLAN) error
	// SetFloodPeers makes the device's flood entries exactly peers.
	SetFloodPeers(ctx context.Context, dev string, peers []string) error
	// SetBridgeMAC sets the bridge's MAC address.
	SetBridgeMAC(ctx context.Context, bridge, mac string) error
	// ListVXLANs returns the names of the overlay devices present on the host.
	ListVXLANs(ctx context.Context) ([]string, error)
	// DeleteVXLAN removes the device. Deleting an absent device is not an error.
	DeleteVXLAN(ctx context.Context, name string) error
}

// vxlanName derives the overlay device name of a VPC, like bridgeName does for
// its bridge.
func vxlanName(uid string) string {
	return ifaceName("vx-", uid)
}

// vniFor derives a VPC's VNI from its UID, so every host computes the same one
// without coordinating. 0 is reserved and never returned.
func vniFor(uid string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(uid))
	if vni := h.Sum32() & 0xFFFFFF; vni != 0 {
		return vni
	}
	return 1
}

// gatewayMAC derives the VPC bridge's MAC from its UID: locally administered
// and unicast (0x02 in the first octet), identical on every host.
func gatewayMAC(uid string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(uid))
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], h.Sum64())
	return fmt.Sprintf("02:%02x:%02x:%02x:%02x:%02x", b[3], b[4], b[5], b[6], b[7])
}

// OverlayReconciler joins this host's VPC bridges to the other nodes'. It is a
// no-op without a node identity or before the node is registered: a lone host
// has no peer to reach.
type OverlayReconciler struct {
	vpcs     *VPCRegistry
	nodes    *NodeRegistry
	net      OverlayBackend
	nodeName string
}

// NewOverlayReconciler returns an overlay pass for the node named nodeName; an
// empty nodeName disables it.
func NewOverlayReconciler(vpcs *VPCRegistry, nodes *NodeRegistry, net OverlayBackend, nodeName string) *OverlayReconciler {
	return &OverlayReconciler{vpcs: vpcs, nodes: nodes, net: net, nodeName: nodeName}
}

// Name identifies the reconcile pass.
func (r *OverlayReconciler) Name() string { return "overlay" }

// ReconcileAll ensures one overlay device per live VPC, pointed at every other
// node, and removes the devices of VPCs that no longer exist.
func (r *OverlayReconciler) ReconcileAll(ctx context.Context) error {
	if r.nodeName == "" {
		return nil
	}
	nodes, err := r.nodes.List()
	if err != nil {
		return fmt.Errorf("manager: list nodes: %w", err)
	}
	local := ""
	for i := range nodes {
		if nodes[i].Metadata.UID == r.nodeName {
			local = nodes[i].Spec.Address
		}
	}
	if local == "" {
		// Not registered yet: the underlay address is unknown.
		return nil
	}
	var peers []string
	for i := range nodes {
		if addr := nodes[i].Spec.Address; addr != "" && addr != local && !slices.Contains(peers, addr) {
			peers = append(peers, addr)
		}
	}
	slices.Sort(peers)

	vpcs, err := r.vpcs.List()
	if err != nil {
		return fmt.Errorf("manager: list vpcs: %w", err)
	}
	var errs []error
	want := map[string]bool{}
	for i := range vpcs {
		vpc := &vpcs[i]
		if vpc.Metadata.IsDeleting() {
			continue
		}
		uid := vpc.Metadata.UID
		v := VXLAN{Name: vxlanName(uid), VNI: vniFor(uid), Local: local, Bridge: bridgeName(uid), GatewayMAC: gatewayMAC(uid), LBMAC: lbMAC(uid)}
		want[v.Name] = true
		if err := r.ensure(ctx, v, peers); err != nil {
			errs = append(errs, fmt.Errorf("vpc %s: %w", uid, err))
		}
	}

	present, err := r.net.ListVXLANs(ctx)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, name := range present {
		if !want[name] {
			if err := r.net.DeleteVXLAN(ctx, name); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (r *OverlayReconciler) ensure(ctx context.Context, v VXLAN, peers []string) error {
	if err := r.net.SetBridgeMAC(ctx, v.Bridge, v.GatewayMAC); err != nil {
		return err
	}
	if err := r.net.EnsureVXLAN(ctx, v); err != nil {
		return err
	}
	return r.net.SetFloodPeers(ctx, v.Name, peers)
}

// ExecOverlay is the OverlayBackend that shells out to iproute2.
type ExecOverlay struct {
	run Runner
}

// NewExecOverlay returns an ExecOverlay using the real command runner.
func NewExecOverlay() *ExecOverlay {
	return &ExecOverlay{run: defaultRun}
}

// NewExecOverlayWithRunner returns an ExecOverlay driven by a custom runner,
// for tests.
func NewExecOverlayWithRunner(run Runner) *ExecOverlay {
	return &ExecOverlay{run: run}
}

// EnsureVXLAN creates, reconfigures and attaches the overlay device.
func (b *ExecOverlay) EnsureVXLAN(ctx context.Context, v VXLAN) error {
	out, err := b.run(ctx, "ip", "-d", "link", "show", v.Name)
	switch {
	case err == nil && !strings.Contains(out, fmt.Sprintf("vxlan id %d local %s ", v.VNI, v.Local)):
		// The node's address or the VNI changed: both are fixed at creation.
		if err := b.DeleteVXLAN(ctx, v.Name); err != nil {
			return err
		}
		fallthrough
	case err != nil:
		if err != nil && !strings.Contains(out, "does not exist") && !strings.Contains(out, "Cannot find device") {
			return fmt.Errorf("manager: show vxlan %q: %w: %s", v.Name, err, strings.TrimSpace(out))
		}
		if out, err := b.run(ctx, "ip", "link", "add", v.Name, "type", "vxlan",
			"id", strconv.FormatUint(uint64(v.VNI), 10), "local", v.Local, "dstport", strconv.Itoa(vxlanPort)); err != nil {
			return fmt.Errorf("manager: add vxlan %q: %w: %s", v.Name, err, strings.TrimSpace(out))
		}
	}
	// `tc ... replace` with a fixed pref and handle is idempotent.
	for _, step := range [][]string{
		{"ip", "link", "set", v.Name, "mtu", strconv.Itoa(overlayMTU)},
		{"ip", "link", "set", v.Name, "master", v.Bridge},
		{"tc", "qdisc", "replace", "dev", v.Name, "clsact"},
		{"tc", "filter", "replace", "dev", v.Name, "egress", "pref", "1", "handle", "1",
			"protocol", "all", "flower", "src_mac", v.GatewayMAC, "action", "drop"},
		{"tc", "filter", "replace", "dev", v.Name, "egress", "pref", "2", "handle", "2",
			"protocol", "all", "flower", "src_mac", v.LBMAC, "action", "drop"},
		{"ip", "link", "set", v.Name, "up"},
	} {
		if out, err := b.run(ctx, step[0], step[1:]...); err != nil {
			return fmt.Errorf("manager: vxlan %q %v: %w: %s", v.Name, step, err, strings.TrimSpace(out))
		}
	}
	return nil
}

// SetFloodPeers adds the missing flood entries and removes the stale ones.
func (b *ExecOverlay) SetFloodPeers(ctx context.Context, dev string, peers []string) error {
	out, err := b.run(ctx, "bridge", "fdb", "show", "dev", dev)
	if err != nil {
		return fmt.Errorf("manager: show fdb of %q: %w: %s", dev, err, strings.TrimSpace(out))
	}
	var current []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == floodMAC && f[1] == "dst" {
			current = append(current, f[2])
		}
	}
	for _, p := range peers {
		if !slices.Contains(current, p) {
			if out, err := b.run(ctx, "bridge", "fdb", "append", floodMAC, "dev", dev, "dst", p); err != nil {
				return fmt.Errorf("manager: add flood peer %s on %q: %w: %s", p, dev, err, strings.TrimSpace(out))
			}
		}
	}
	for _, c := range current {
		if !slices.Contains(peers, c) {
			if out, err := b.run(ctx, "bridge", "fdb", "del", floodMAC, "dev", dev, "dst", c); err != nil {
				return fmt.Errorf("manager: remove flood peer %s on %q: %w: %s", c, dev, err, strings.TrimSpace(out))
			}
		}
	}
	return nil
}

// SetBridgeMAC pins the bridge's MAC address.
func (b *ExecOverlay) SetBridgeMAC(ctx context.Context, bridge, mac string) error {
	if out, err := b.run(ctx, "ip", "link", "set", bridge, "address", mac); err != nil {
		return fmt.Errorf("manager: set %q address %s: %w: %s", bridge, mac, err, strings.TrimSpace(out))
	}
	return nil
}

// ListVXLANs returns the overlay devices, recognised by their vx- prefix.
func (b *ExecOverlay) ListVXLANs(ctx context.Context) ([]string, error) {
	out, err := b.run(ctx, "ip", "-o", "link", "show", "type", "vxlan")
	if err != nil {
		return nil, fmt.Errorf("manager: list vxlans: %w: %s", err, strings.TrimSpace(out))
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		// "12: vx-abc: <BROADCAST,...> mtu 1450 ..."
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimSuffix(f[1], ":"), "@")
		if strings.HasPrefix(name, "vx-") {
			names = append(names, name)
		}
	}
	return names, nil
}

// DeleteVXLAN removes the device if present.
func (b *ExecOverlay) DeleteVXLAN(ctx context.Context, name string) error {
	out, err := b.run(ctx, "ip", "link", "del", name)
	if err != nil && !strings.Contains(out, "Cannot find device") && !strings.Contains(out, "does not exist") {
		return fmt.Errorf("manager: delete vxlan %q: %w: %s", name, err, strings.TrimSpace(out))
	}
	return nil
}
