// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
)

func TestVPCGetsALoadBalancerNamespaceRemovedOnDelete(t *testing.T) {
	t.Parallel()

	vpcs := newVPCRegistry(t)
	be := newFakeBackend()
	putVPC(t, vpcs, "vpc1", false)
	r := manager.NewVPCReconciler(vpcs, be)
	if err := r.Reconcile(context.Background(), "vpc1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if be.nodePorts["vpc1"] != "br-vpc1" {
		t.Fatalf("node port: %v", be.nodePorts)
	}

	vpc, _ := vpcs.Get("vpc1")
	putVPC(t, vpcs, "vpc1", true)
	deleting, _ := vpcs.Get("vpc1")
	deleting.Metadata.Finalizers = vpc.Metadata.Finalizers
	if err := vpcs.Put(deleting); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}
	if err := r.Reconcile(context.Background(), "vpc1"); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if _, ok := be.nodePorts["vpc1"]; ok {
		t.Fatalf("node port left behind: %v", be.nodePorts)
	}
}

func TestSubnetTakesTheHostsRankOnTheNodePort(t *testing.T) {
	t.Parallel()

	vpcs := newVPCRegistry(t)
	if err := vpcs.Put(&resource.VPC{
		Metadata: resource.ObjectMeta{UID: "vpc1", Generation: 1},
		Spec:     resource.VPCSpec{CIDR: "10.0.0.0/16"},
		Status:   resource.VPCStatus{BridgeName: "br-vpc1"},
	}); err != nil {
		t.Fatalf("seed vpc: %v", err)
	}
	subnets := newSubnetRegistry(t)
	if err := subnets.Put(&resource.Subnet{
		Metadata: resource.ObjectMeta{UID: "sn1", Generation: 1},
		Spec:     resource.SubnetSpec{VPCID: "vpc1", CIDR: "10.0.1.0/24", Type: "private"},
	}); err != nil {
		t.Fatalf("seed subnet: %v", err)
	}
	nodes := newNodeRegistry(t)
	putNode(t, nodes, "node-a", "192.168.0.1")
	putNode(t, nodes, "node-b", "192.168.0.2")

	be := newFakeBackend()
	// A rank-0 address left from before node-a registered.
	be.nodeAddrs = map[string]bool{"vpc1 10.0.1.254/24": true}
	r := manager.NewSubnetReconciler(subnets, vpcs, be).WithNodeIdentity(nodes, "node-b")
	if err := r.Reconcile(context.Background(), "sn1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !be.gateways["br-vpc1 10.0.1.1/24"] {
		t.Fatalf("gateway not assigned: %v", be.gateways)
	}
	if !be.nodeAddrs["vpc1 10.0.1.253/24"] || be.nodeAddrs["vpc1 10.0.1.254/24"] {
		t.Fatalf("node-b (rank 1) should hold only .253 on the node port: %v", be.nodeAddrs)
	}
}

// addrShow answers `ip -o [-4] addr show` with out and records every call.
type addrShow struct {
	out   string
	calls []string
}

func (a *addrShow) run(_ context.Context, name string, args ...string) (string, error) {
	cmd := name + " " + strings.Join(args, " ")
	a.calls = append(a.calls, cmd)
	if strings.HasPrefix(cmd, "ip -o -4 addr show") || strings.HasPrefix(cmd, "ip -o addr show") {
		return a.out, nil
	}
	return "", nil
}

func (a *addrShow) saw(prefix string) bool {
	for _, c := range a.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func TestEnsureGatewayAddressKeepsThePrefixRoute(t *testing.T) {
	t.Parallel()

	const flagged = "5: br-1    inet 10.0.1.1/24 scope global noprefixroute br-1\\       valid_lft forever"
	const plain = "5: br-1    inet 10.0.1.1/24 scope global br-1\\       valid_lft forever"
	for _, tc := range []struct {
		name, out     string
		wantDel, want bool
	}{
		{"absent", "", false, true},
		{"left without its route by an agent before the namespace", flagged, true, true},
		{"already assigned", plain, false, false},
	} {
		a := &addrShow{out: tc.out}
		if err := manager.NewExecBackendWithRunner(a.run).EnsureGatewayAddress(context.Background(), "br-1", "10.0.1.1/24"); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := a.saw("ip addr del 10.0.1.1/24 dev br-1"); got != tc.wantDel {
			t.Fatalf("%s: del=%v, calls %v", tc.name, got, a.calls)
		}
		if got := a.saw("ip addr add 10.0.1.1/24 dev br-1"); got != tc.want {
			t.Fatalf("%s: add=%v, calls %v", tc.name, got, a.calls)
		}
	}
}

func TestEnsureNodePortPlugsTheNamespaceIntoTheBridge(t *testing.T) {
	t.Parallel()

	h := &nsHost{}
	if err := manager.NewExecBackendWithRunner(h.run).EnsureNodePort(context.Background(), "vpc1", "br-vpc1"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	for _, want := range []string{
		"ip link add nb-vpc1 mtu 1450 type veth peer name np0 mtu 1450 netns 4242",
		"ip link add la-vpc1 mtu 1450 type veth peer name la0 mtu 1450 netns 4242",
		"ip link set nb-vpc1 master br-vpc1",
		"ip link set la-vpc1 master br-vpc1",
		"sysctl -w net.ipv4.conf.br-vpc1.arp_ignore=1",
		inNS + "sysctl -w net.ipv4.conf.np0.arp_ignore=1",
		inNS + "sysctl -w net.ipv4.conf.la0.arp_ignore=1",
		inNS + "sysctl -w net.ipv4.conf.all.rp_filter=0",
	} {
		if !slices.Contains(h.calls, want) {
			t.Fatalf("missing %q in\n%s", want, h.joined())
		}
	}
	// The anycast port takes the same MAC on every host, before coming up.
	mac, up := -1, -1
	for i, c := range h.calls {
		if strings.HasPrefix(c, inNS+"ip link set la0 address 02:") {
			mac = i
		}
		if c == inNS+"ip link set la0 up" {
			up = i
		}
	}
	if mac < 0 || up < mac {
		t.Fatalf("la0 must get its anycast MAC before coming up:\n%s", h.joined())
	}
}

func TestEnsureNodePortRemovesTheHostNodePortLeftBehind(t *testing.T) {
	t.Parallel()

	h := &nsHost{}
	run := func(ctx context.Context, name string, args ...string) (string, error) {
		if name+" "+strings.Join(args, " ") == "ip link show np-vpc1" {
			h.calls = append(h.calls, "ip link show np-vpc1")
			return "", nil
		}
		return h.run(ctx, name, args...)
	}
	if err := manager.NewExecBackendWithRunner(run).EnsureNodePort(context.Background(), "vpc1", "br-vpc1"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !slices.Contains(h.calls, "ip link del np-vpc1") {
		t.Fatalf("the host's node port must go:\n%s", h.joined())
	}
}

func TestNodeAddressesLiveInTheNamespace(t *testing.T) {
	t.Parallel()

	h := &nsHost{}
	if err := manager.NewExecBackendWithRunner(h.run).EnsureNodeAddress(context.Background(), "vpc1", "10.0.1.254/24"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(h.calls, inNS+"ip addr replace 10.0.1.254/24 dev np0") {
		t.Fatalf("node address not on np0:\n%s", h.joined())
	}
}

func TestEnsureNodePortStartsAStoppedNamespace(t *testing.T) {
	t.Parallel()

	h := &nsHost{}
	started := false
	run := func(ctx context.Context, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		switch {
		case strings.HasPrefix(cmd, "systemctl show") && !started:
			h.calls = append(h.calls, cmd)
			return "0\n", nil
		case cmd == "systemctl start infra-netns@lb-vpc1.service":
			started = true
		}
		return h.run(ctx, name, args...)
	}
	if err := manager.NewExecBackendWithRunner(run).EnsureNodePort(context.Background(), "vpc1", "br-vpc1"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !started || !strings.Contains(h.joined(), "netns 4242") {
		t.Fatalf("the holder must be started and its namespace used:\n%s", h.joined())
	}
}
