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

// serviceLog records every virtual service ensured and deleted.
type serviceLog struct {
	ensured []string // "vip@bridge"
	deleted []string // "vip@bridge"
}

func (l *serviceLog) EnsureService(_ context.Context, vip string, _ int, _, _, bridge, _ string, _ []manager.LBRealServer) error {
	l.ensured = append(l.ensured, vip+"@"+bridge)
	return nil
}

func (l *serviceLog) DeleteService(_ context.Context, vip string, _ int, _, bridge string) error {
	l.deleted = append(l.deleted, vip+"@"+bridge)
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
	if !slices.Equal(edge.ensured, []string{"10.0.0.10@br-vpc1", "203.0.113.10@lo"}) {
		t.Fatalf("edge ensured %v, want the VIP on the bridge and the public address on lo", edge.ensured)
	}
	if got, _ := env.lbs.Get("lb"); got.Status.PublicAddress != "203.0.113.10" {
		t.Fatalf("public address %q", got.Status.PublicAddress)
	}

	inner := &serviceLog{}
	if err := env.reconciler(inner).Reconcile(context.Background(), "lb"); err != nil {
		t.Fatalf("non-edge: %v", err)
	}
	if !slices.Equal(inner.ensured, []string{"10.0.0.10@br-vpc1"}) {
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
	if !slices.Equal(l.ensured, []string{"10.0.0.10@br-vpc1"}) || got.Status.Phase != resource.PhaseReady {
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
	if !slices.Contains(l.deleted, "203.0.113.10@lo") || l.ensured[len(l.ensured)-1] != "203.0.113.11@lo" {
		t.Fatalf("moving to pub-b: ensured %v deleted %v", l.ensured, l.deleted)
	}

	lb, _ = env.lbs.Get("lb")
	now := time.Now()
	lb.Metadata.DeletionTimestamp = &now
	_ = env.lbs.Put(lb)
	if err := r.Reconcile(context.Background(), "lb"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(l.deleted, "203.0.113.11@lo") {
		t.Fatalf("deleting the load balancer must drop its public address: %v", l.deleted)
	}
}
