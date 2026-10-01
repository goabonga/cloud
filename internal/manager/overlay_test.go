// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
)

// fakeOverlay records what the overlay reconciler asks of the kernel.
type fakeOverlay struct {
	vxlans  map[string]manager.VXLAN
	peers   map[string][]string
	macs    map[string]string
	deleted []string
}

func newFakeOverlay(present ...string) *fakeOverlay {
	f := &fakeOverlay{vxlans: map[string]manager.VXLAN{}, peers: map[string][]string{}, macs: map[string]string{}}
	for _, name := range present {
		f.vxlans[name] = manager.VXLAN{Name: name}
	}
	return f
}

func (f *fakeOverlay) EnsureVXLAN(_ context.Context, v manager.VXLAN) error {
	f.vxlans[v.Name] = v
	return nil
}

func (f *fakeOverlay) SetFloodPeers(_ context.Context, dev string, peers []string) error {
	f.peers[dev] = slices.Clone(peers)
	return nil
}

func (f *fakeOverlay) SetBridgeMAC(_ context.Context, bridge, mac string) error {
	f.macs[bridge] = mac
	return nil
}

func (f *fakeOverlay) ListVXLANs(_ context.Context) ([]string, error) {
	var names []string
	for name := range f.vxlans {
		names = append(names, name)
	}
	return names, nil
}

func (f *fakeOverlay) DeleteVXLAN(_ context.Context, name string) error {
	delete(f.vxlans, name)
	f.deleted = append(f.deleted, name)
	return nil
}

func putNode(t *testing.T, reg *manager.NodeRegistry, uid, addr string) {
	t.Helper()
	if err := reg.Put(&resource.Node{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.NodeSpec{Hostname: uid, Address: addr, Capacity: resource.NodeCapacity{CPUs: 2, MemoryMB: 1024}},
	}); err != nil {
		t.Fatalf("seed node %s: %v", uid, err)
	}
}

func putVPC(t *testing.T, reg *manager.VPCRegistry, uid string, deleting bool) {
	t.Helper()
	vpc := &resource.VPC{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.VPCSpec{CIDR: "10.20.0.0/16"},
	}
	if deleting {
		now := time.Now()
		vpc.Metadata.DeletionTimestamp = &now
	}
	if err := reg.Put(vpc); err != nil {
		t.Fatalf("seed vpc %s: %v", uid, err)
	}
}

// onlyVXLAN returns the single overlay device the fake holds.
func onlyVXLAN(t *testing.T, f *fakeOverlay) manager.VXLAN {
	t.Helper()
	if len(f.vxlans) != 1 {
		t.Fatalf("expected one vxlan, got %v", f.vxlans)
	}
	for _, v := range f.vxlans {
		return v
	}
	return manager.VXLAN{}
}

func TestOverlayJoinsTwoNodesOnTheSameSegment(t *testing.T) {
	t.Parallel()

	nodes := newNodeRegistry(t)
	putNode(t, nodes, "node-a", "192.168.122.21")
	putNode(t, nodes, "node-b", "192.168.122.22")
	vpcs := newVPCRegistry(t)
	putVPC(t, vpcs, "vpc-1", false)

	a, b := newFakeOverlay(), newFakeOverlay()
	if err := manager.NewOverlayReconciler(vpcs, nodes, a, "node-a").ReconcileAll(context.Background()); err != nil {
		t.Fatalf("node-a: %v", err)
	}
	if err := manager.NewOverlayReconciler(vpcs, nodes, b, "node-b").ReconcileAll(context.Background()); err != nil {
		t.Fatalf("node-b: %v", err)
	}

	va, vb := onlyVXLAN(t, a), onlyVXLAN(t, b)
	if va.Name != vb.Name || va.VNI != vb.VNI || va.Bridge != vb.Bridge || va.GatewayMAC != vb.GatewayMAC || va.LBMAC != vb.LBMAC {
		t.Fatalf("hosts disagree on the overlay: %+v vs %+v", va, vb)
	}
	if va.VNI == 0 || va.VNI > 0xFFFFFF {
		t.Fatalf("VNI %d is not a valid 24-bit identifier", va.VNI)
	}
	if va.Local != "192.168.122.21" || vb.Local != "192.168.122.22" {
		t.Fatalf("tunnel sources: a=%s b=%s", va.Local, vb.Local)
	}
	if !slices.Equal(a.peers[va.Name], []string{"192.168.122.22"}) || !slices.Equal(b.peers[vb.Name], []string{"192.168.122.21"}) {
		t.Fatalf("peers: a=%v b=%v", a.peers, b.peers)
	}
	// Anycast gateway: the same locally administered unicast MAC on both bridges.
	macA, macB := a.macs[va.Bridge], b.macs[vb.Bridge]
	if macA == "" || macA != macB || !strings.HasPrefix(macA, "02:") || va.GatewayMAC != macA {
		t.Fatalf("bridge MACs: a=%q b=%q", macA, macB)
	}
	// The load balancers' anycast MAC is another one, also kept off the overlay.
	if !strings.HasPrefix(va.LBMAC, "02:") || va.LBMAC == va.GatewayMAC {
		t.Fatalf("load-balancer MAC %q, gateway MAC %q", va.LBMAC, va.GatewayMAC)
	}
}

func TestOverlayRemovesDevicesOfGoneAndDeletingVPCs(t *testing.T) {
	t.Parallel()

	nodes := newNodeRegistry(t)
	putNode(t, nodes, "node-a", "10.0.0.1")
	putNode(t, nodes, "node-b", "10.0.0.2")
	vpcs := newVPCRegistry(t)
	putVPC(t, vpcs, "vpc-live", false)
	putVPC(t, vpcs, "vpc-going", true)

	// First pass: learn the names the reconciler gives both VPCs' devices.
	probe := newFakeOverlay()
	putVPC(t, vpcs, "vpc-going", false)
	if err := manager.NewOverlayReconciler(vpcs, nodes, probe, "node-a").ReconcileAll(context.Background()); err != nil {
		t.Fatalf("probe: %v", err)
	}
	var names []string
	for name := range probe.vxlans {
		names = append(names, name)
	}
	putVPC(t, vpcs, "vpc-going", true)

	// A device left over from a VPC removed while the agent was down, plus
	// the devices of both VPCs.
	f := newFakeOverlay(append(names, "vx-stale")...)
	if err := manager.NewOverlayReconciler(vpcs, nodes, f, "node-a").ReconcileAll(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(f.deleted) != 2 || !slices.Contains(f.deleted, "vx-stale") {
		t.Fatalf("deleted %v, want vx-stale and the deleting VPC's device", f.deleted)
	}
	if len(f.vxlans) != 1 {
		t.Fatalf("remaining devices %v, want only the live VPC's", f.vxlans)
	}
}

func TestOverlayIsANoopWithoutARegisteredIdentity(t *testing.T) {
	t.Parallel()

	nodes := newNodeRegistry(t)
	putNode(t, nodes, "node-b", "10.0.0.2")
	vpcs := newVPCRegistry(t)
	putVPC(t, vpcs, "vpc-1", false)

	for _, name := range []string{"", "node-unregistered"} {
		f := newFakeOverlay()
		if err := manager.NewOverlayReconciler(vpcs, nodes, f, name).ReconcileAll(context.Background()); err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if len(f.vxlans) != 0 || len(f.macs) != 0 {
			t.Fatalf("%q: touched the kernel: %+v", name, f)
		}
	}
}

// overlayKernel answers the iproute2 commands ExecOverlay issues, keeping one
// VXLAN device and its flood entries.
type overlayKernel struct {
	calls   [][]string
	details string // `ip -d link show` output; empty when the device is absent
	fdb     string // `bridge fdb show` output
}

func (k *overlayKernel) run(_ context.Context, name string, args ...string) (string, error) {
	k.calls = append(k.calls, append([]string{name}, args...))
	cmd := name + " " + strings.Join(args, " ")
	switch {
	case strings.HasPrefix(cmd, "ip -d link show "):
		if k.details == "" {
			return `Device "` + args[3] + `" does not exist.`, errors.New("exit status 1")
		}
		return k.details, nil
	case strings.HasPrefix(cmd, "bridge fdb show"):
		return k.fdb, nil
	}
	return "", nil
}

func (k *overlayKernel) saw(want ...string) bool {
	for _, c := range k.calls {
		if strings.Join(c, " ") == strings.Join(want, " ") {
			return true
		}
	}
	return false
}

func TestExecOverlayCreatesAndAttachesTheDevice(t *testing.T) {
	t.Parallel()

	k := &overlayKernel{}
	be := manager.NewExecOverlayWithRunner(k.run)
	v := manager.VXLAN{Name: "vx-1", VNI: 42, Local: "10.0.0.1", Bridge: "br-1", GatewayMAC: "02:aa:bb:cc:dd:ee", LBMAC: "02:11:22:33:44:55"}
	if err := be.EnsureVXLAN(context.Background(), v); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	for _, want := range [][]string{
		{"ip", "link", "add", "vx-1", "type", "vxlan", "id", "42", "local", "10.0.0.1", "dstport", "4789"},
		{"ip", "link", "set", "vx-1", "mtu", "1450"},
		{"ip", "link", "set", "vx-1", "master", "br-1"},
		{"tc", "qdisc", "replace", "dev", "vx-1", "clsact"},
		{"tc", "filter", "replace", "dev", "vx-1", "egress", "pref", "1", "handle", "1", "protocol", "all", "flower", "src_mac", "02:aa:bb:cc:dd:ee", "action", "drop"},
		{"tc", "filter", "replace", "dev", "vx-1", "egress", "pref", "2", "handle", "2", "protocol", "all", "flower", "src_mac", "02:11:22:33:44:55", "action", "drop"},
		{"ip", "link", "set", "vx-1", "up"},
	} {
		if !k.saw(want...) {
			t.Fatalf("missing %v in %v", want, k.calls)
		}
	}
}

func TestExecOverlayKeepsAMatchingDeviceAndRecreatesAStaleOne(t *testing.T) {
	t.Parallel()

	v := manager.VXLAN{Name: "vx-1", VNI: 42, Local: "10.0.0.1", Bridge: "br-1"}
	matching := "7: vx-1: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1450\n    vxlan id 42 local 10.0.0.1 srcport 0 0 dstport 4789 ttl auto\n"

	k := &overlayKernel{details: matching}
	if err := manager.NewExecOverlayWithRunner(k.run).EnsureVXLAN(context.Background(), v); err != nil {
		t.Fatalf("ensure matching: %v", err)
	}
	if k.saw("ip", "link", "del", "vx-1") || k.saw("ip", "link", "add", "vx-1", "type", "vxlan", "id", "42", "local", "10.0.0.1", "dstport", "4789") {
		t.Fatalf("a matching device must be left in place: %v", k.calls)
	}

	// The node's address changed since the device was created.
	k = &overlayKernel{details: strings.Replace(matching, "local 10.0.0.1", "local 10.0.0.9", 1)}
	if err := manager.NewExecOverlayWithRunner(k.run).EnsureVXLAN(context.Background(), v); err != nil {
		t.Fatalf("ensure stale: %v", err)
	}
	if !k.saw("ip", "link", "del", "vx-1") || !k.saw("ip", "link", "add", "vx-1", "type", "vxlan", "id", "42", "local", "10.0.0.1", "dstport", "4789") {
		t.Fatalf("a stale device must be recreated: %v", k.calls)
	}
}

func TestExecOverlaySyncsFloodPeers(t *testing.T) {
	t.Parallel()

	k := &overlayKernel{fdb: "" +
		"00:00:00:00:00:00 dst 10.0.0.2 self permanent\n" +
		"00:00:00:00:00:00 dst 10.0.0.9 self permanent\n" +
		"aa:bb:cc:dd:ee:ff dst 10.0.0.2 self\n"}
	be := manager.NewExecOverlayWithRunner(k.run)
	if err := be.SetFloodPeers(context.Background(), "vx-1", []string{"10.0.0.2", "10.0.0.3"}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if !k.saw("bridge", "fdb", "append", "00:00:00:00:00:00", "dev", "vx-1", "dst", "10.0.0.3") {
		t.Fatalf("missing peer not added: %v", k.calls)
	}
	if !k.saw("bridge", "fdb", "del", "00:00:00:00:00:00", "dev", "vx-1", "dst", "10.0.0.9") {
		t.Fatalf("departed peer not removed: %v", k.calls)
	}
	if k.saw("bridge", "fdb", "append", "00:00:00:00:00:00", "dev", "vx-1", "dst", "10.0.0.2") {
		t.Fatalf("existing peer re-added: %v", k.calls)
	}
}

func TestExecOverlayListsOnlyItsDevices(t *testing.T) {
	t.Parallel()

	out := "" +
		"7: vx-1: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1450 qdisc noqueue master br-1 state UNKNOWN\\    link/ether 0a:...\n" +
		"9: flannel.1: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1450 qdisc noqueue state UNKNOWN\\    link/ether 1a:...\n"
	be := manager.NewExecOverlayWithRunner(func(context.Context, string, ...string) (string, error) { return out, nil })
	names, err := be.ListVXLANs(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !slices.Equal(names, []string{"vx-1"}) {
		t.Fatalf("names = %v, want [vx-1] (foreign vxlans left alone)", names)
	}
}
