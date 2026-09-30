// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
)

func TestVPCGivesTheHostANodePortAndRemovesItOnDelete(t *testing.T) {
	t.Parallel()

	vpcs := newVPCRegistry(t)
	be := newFakeBackend()
	putVPC(t, vpcs, "vpc1", false)
	r := manager.NewVPCReconciler(vpcs, be)
	if err := r.Reconcile(context.Background(), "vpc1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if be.nodePorts["np-vpc1"] != "br-vpc1 nb-vpc1" {
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
	if _, ok := be.nodePorts["np-vpc1"]; ok {
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
	be.addresses["np-vpc1 10.0.1.254/24"] = true
	r := manager.NewSubnetReconciler(subnets, vpcs, be).WithNodeIdentity(nodes, "node-b")
	if err := r.Reconcile(context.Background(), "sn1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !be.gateways["br-vpc1 10.0.1.1/24"] {
		t.Fatalf("gateway must be assigned without its prefix route: %v", be.gateways)
	}
	if !be.addresses["np-vpc1 10.0.1.253/24"] || be.addresses["np-vpc1 10.0.1.254/24"] {
		t.Fatalf("node-b (rank 1) should hold only .253 on the node port: %v", be.addresses)
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

func TestEnsureGatewayAddressSetsNoPrefixRoute(t *testing.T) {
	t.Parallel()

	const flagged = "5: br-1    inet 10.0.1.1/24 scope global noprefixroute br-1\\       valid_lft forever"
	const plain = "5: br-1    inet 10.0.1.1/24 scope global br-1\\       valid_lft forever"
	for _, tc := range []struct {
		name, out     string
		wantDel, want bool
	}{
		{"absent", "", false, true},
		{"assigned before the node port", plain, true, true},
		{"already flagged", flagged, false, false},
	} {
		a := &addrShow{out: tc.out}
		if err := manager.NewExecBackendWithRunner(a.run).EnsureGatewayAddress(context.Background(), "br-1", "10.0.1.1/24"); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := a.saw("ip addr del 10.0.1.1/24 dev br-1"); got != tc.wantDel {
			t.Fatalf("%s: del=%v, calls %v", tc.name, got, a.calls)
		}
		if got := a.saw("ip addr add 10.0.1.1/24 dev br-1 noprefixroute"); got != tc.want {
			t.Fatalf("%s: add=%v, calls %v", tc.name, got, a.calls)
		}
	}
}

func TestEnsureNodePortCreatesAnARPQuietPair(t *testing.T) {
	t.Parallel()

	var calls []string
	run := func(_ context.Context, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		calls = append(calls, cmd)
		if cmd == "ip link show np-1" {
			return `Device "np-1" does not exist.`, errors.New("exit status 1")
		}
		return "", nil
	}
	if err := manager.NewExecBackendWithRunner(run).EnsureNodePort(context.Background(), "br-1", "np-1", "nb-1"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	for _, want := range []string{
		"ip link add np-1 mtu 1450 type veth peer name nb-1 mtu 1450",
		"ip link set nb-1 master br-1",
		"ip link set nb-1 up",
		"ip link set np-1 up",
		"sysctl -w net.ipv4.conf.br-1.arp_ignore=1",
		"sysctl -w net.ipv4.conf.np-1.arp_ignore=1",
	} {
		found := false
		for _, c := range calls {
			found = found || c == want
		}
		if !found {
			t.Fatalf("missing %q in %v", want, calls)
		}
	}
}
