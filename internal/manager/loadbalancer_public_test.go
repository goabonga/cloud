// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
)

// serviceLog records every virtual service ensured and deleted: "vip@<vpc>"
// for a VIP, "addr@public" for a public address, "addr@host:<iface>" for a
// service removed from the host.
type serviceLog struct {
	ensured []string
	deleted []string
}

func (l *serviceLog) EnsureService(_ context.Context, vpcID, _, vip string, _ int, _, _ string, _ []manager.LBRealServer) error {
	l.ensured = append(l.ensured, vip+"@"+vpcID)
	return nil
}

func (l *serviceLog) DeleteService(_ context.Context, vpcID, _, vip string, _ int, _ string) error {
	l.deleted = append(l.deleted, vip+"@"+vpcID)
	return nil
}

func (l *serviceLog) EnsurePublicService(_ context.Context, _, addr string, _ int, _, _ string, _ []manager.LBRealServer) error {
	l.ensured = append(l.ensured, addr+"@public")
	return nil
}

func (l *serviceLog) DeletePublicService(_ context.Context, _, addr string, _ int, _ string) error {
	l.deleted = append(l.deleted, addr+"@public")
	return nil
}

func (l *serviceLog) DeleteHostService(_ context.Context, addr string, _ int, _, iface string) error {
	l.deleted = append(l.deleted, addr+"@host:"+iface)
	return nil
}

func (env *lbEnv) putPublicLB(t *testing.T, uid, vip, publicIPID string) {
	t.Helper()
	if err := env.lbs.Put(&resource.LoadBalancer{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.LoadBalancerSpec{VPCID: "vpc-1", Address: vip, Port: 80, Protocol: "tcp", PublicIPID: publicIPID},
	}); err != nil {
		t.Fatal(err)
	}
}

func (env *lbEnv) putResolvedPublicIP(t *testing.T, uid, addr string) {
	t.Helper()
	ip := &resource.IPAddress{Metadata: resource.ObjectMeta{UID: uid, Generation: 1}, Spec: resource.IPAddressSpec{Type: "public"}}
	ip.Status.Address = addr
	if err := env.ips.Put(ip); err != nil {
		t.Fatal(err)
	}
}

func TestLoadBalancerServesItsPublicAddressOnTheEdges(t *testing.T) {
	t.Parallel()

	env := newLBEnv(t)
	env.putResolvedPublicIP(t, "pub", "203.0.113.10")
	env.putPublicLB(t, "lb", "10.0.0.10", "pub")

	edge := &serviceLog{}
	if err := env.reconciler(edge).AsEdge(env.ips).Reconcile(context.Background(), "lb"); err != nil {
		t.Fatalf("edge: %v", err)
	}
	if !slices.Equal(edge.ensured, []string{"10.0.0.10@vpc-1", "203.0.113.10@public"}) {
		t.Fatalf("edge ensured %v, want the VIP and the public address", edge.ensured)
	}
	if got, _ := env.lbs.Get("lb"); got.Status.PublicAddress != "203.0.113.10" {
		t.Fatalf("public address %q", got.Status.PublicAddress)
	}

	inner := &serviceLog{}
	if err := env.reconciler(inner).Reconcile(context.Background(), "lb"); err != nil {
		t.Fatalf("non-edge: %v", err)
	}
	if !slices.Equal(inner.ensured, []string{"10.0.0.10@vpc-1"}) {
		t.Fatalf("a host that is no edge serves only the VIP: %v", inner.ensured)
	}
}

func TestLoadBalancerWaitsForAnUnresolvedPublicAddress(t *testing.T) {
	t.Parallel()

	env := newLBEnv(t)
	env.putResolvedPublicIP(t, "pub", "") // not reserved yet
	env.putPublicLB(t, "lb", "10.0.0.10", "pub")
	l := &serviceLog{}
	if err := env.reconciler(l).AsEdge(env.ips).Reconcile(context.Background(), "lb"); err != nil {
		t.Fatalf("an unresolved public address must not fail the load balancer: %v", err)
	}
	got, _ := env.lbs.Get("lb")
	if !slices.Equal(l.ensured, []string{"10.0.0.10@vpc-1"}) || got.Status.Phase != resource.PhaseReady {
		t.Fatalf("ensured %v, phase %q", l.ensured, got.Status.Phase)
	}
}

func TestLoadBalancerMovesAndDropsItsPublicAddress(t *testing.T) {
	t.Parallel()

	env := newLBEnv(t)
	env.putResolvedPublicIP(t, "pub-a", "203.0.113.10")
	env.putResolvedPublicIP(t, "pub-b", "203.0.113.11")
	env.putPublicLB(t, "lb", "10.0.0.10", "pub-a")
	l := &serviceLog{}
	r := env.reconciler(l).AsEdge(env.ips)
	if err := r.Reconcile(context.Background(), "lb"); err != nil {
		t.Fatal(err)
	}

	lb, _ := env.lbs.Get("lb")
	lb.Spec.PublicIPID = "pub-b"
	lb.Metadata.Generation++
	_ = env.lbs.Put(lb)
	if err := r.Reconcile(context.Background(), "lb"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(l.deleted, "203.0.113.10@public") || l.ensured[len(l.ensured)-1] != "203.0.113.11@public" {
		t.Fatalf("moving to pub-b: ensured %v deleted %v", l.ensured, l.deleted)
	}

	lb, _ = env.lbs.Get("lb")
	now := time.Now()
	lb.Metadata.DeletionTimestamp = &now
	_ = env.lbs.Put(lb)
	if err := r.Reconcile(context.Background(), "lb"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(l.deleted, "203.0.113.11@public") {
		t.Fatalf("deleting the load balancer must drop its public address: %v", l.deleted)
	}
}

func TestLoadBalancerMovesItsHostServiceIntoTheNamespaceOnce(t *testing.T) {
	t.Parallel()

	env := newLBEnv(t)
	env.putResolvedPublicIP(t, "pub", "203.0.113.10")
	env.putPublicLB(t, "lb", "10.0.0.10", "pub")
	// Served before by an agent that realized load balancers in the host.
	lb, _ := env.lbs.Get("lb")
	lb.Status.PublicAddress = "203.0.113.10"
	_ = env.lbs.Put(lb)

	l := &serviceLog{}
	r := env.reconciler(l).AsEdge(env.ips)
	for range 2 {
		if err := r.Reconcile(context.Background(), "lb"); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(l.deleted, []string{"10.0.0.10@host:br-vpc1", "203.0.113.10@host:lo"}) {
		t.Fatalf("deleted %v, want the host's VIP and public address removed once", l.deleted)
	}
}
