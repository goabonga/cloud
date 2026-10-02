// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func newSubnetRegistry(t *testing.T) *manager.SubnetRegistry {
	t.Helper()
	return registry.New[resource.SubnetSpec, resource.SubnetStatus](state.NewFileStore(t.TempDir()), resource.KindSubnet)
}

func seedVPCWithBridge(t *testing.T, reg *manager.VPCRegistry, uid, cidr, bridge string) {
	t.Helper()
	v := &resource.VPC{Metadata: resource.ObjectMeta{UID: uid, Generation: 1}, Spec: resource.VPCSpec{CIDR: cidr}}
	v.Status.BridgeName = bridge
	if err := reg.Put(v); err != nil {
		t.Fatalf("seed vpc: %v", err)
	}
}

func seedSubnet(t *testing.T, reg *manager.SubnetRegistry, uid, vpcID, cidr string) {
	t.Helper()
	if err := reg.Put(&resource.Subnet{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.SubnetSpec{VPCID: vpcID, CIDR: cidr, Type: "public"},
	}); err != nil {
		t.Fatalf("seed subnet: %v", err)
	}
}

func TestSubnetReconcileAssignsGateway(t *testing.T) {
	t.Parallel()

	vpcs := newVPCRegistry(t)
	seedVPCWithBridge(t, vpcs, "vpc-1", "10.0.0.0/16", "br-vpc1")
	subnets := newSubnetRegistry(t)
	be := newFakeBackend()
	seedSubnet(t, subnets, "sn-1", "vpc-1", "10.0.1.0/24")

	rec := manager.NewSubnetReconciler(subnets, vpcs, be)
	if err := rec.Reconcile(context.Background(), "sn-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _ := subnets.Get("sn-1")
	if !got.Status.IsReady() {
		t.Fatalf("phase = %q, want Ready", got.Status.Phase)
	}
	if got.Status.Gateway != "10.0.1.1" {
		t.Fatalf("gateway = %q, want 10.0.1.1", got.Status.Gateway)
	}
	if !got.Metadata.HasFinalizer(resource.SubnetFinalizer) {
		t.Fatal("finalizer should be attached")
	}
	if !be.addresses["br-vpc1 10.0.1.1/24"] {
		t.Fatalf("gateway address not assigned: %v", be.addresses)
	}
}

func TestSubnetPendingWhenVPCBridgeMissing(t *testing.T) {
	t.Parallel()

	vpcs := newVPCRegistry(t)
	// VPC exists but its bridge is not provisioned yet.
	if err := vpcs.Put(&resource.VPC{Metadata: resource.ObjectMeta{UID: "vpc-1"}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}); err != nil {
		t.Fatalf("seed vpc: %v", err)
	}
	subnets := newSubnetRegistry(t)
	seedSubnet(t, subnets, "sn-1", "vpc-1", "10.0.1.0/24")

	rec := manager.NewSubnetReconciler(subnets, vpcs, newFakeBackend())
	if err := rec.Reconcile(context.Background(), "sn-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _ := subnets.Get("sn-1")
	if got.Status.Phase != resource.PhasePending {
		t.Fatalf("phase = %q, want Pending", got.Status.Phase)
	}
}

func TestSubnetVPCNotFound(t *testing.T) {
	t.Parallel()

	subnets := newSubnetRegistry(t)
	seedSubnet(t, subnets, "sn-1", "ghost", "10.0.1.0/24")
	rec := manager.NewSubnetReconciler(subnets, newVPCRegistry(t), newFakeBackend())
	if err := rec.Reconcile(context.Background(), "sn-1"); err == nil {
		t.Fatal("expected error for missing vpc")
	}
	got, _ := subnets.Get("sn-1")
	if got.Status.Phase != resource.PhaseError {
		t.Fatalf("phase = %q, want Error", got.Status.Phase)
	}
}

func TestSubnetFinalize(t *testing.T) {
	t.Parallel()

	vpcs := newVPCRegistry(t)
	seedVPCWithBridge(t, vpcs, "vpc-1", "10.0.0.0/16", "br-vpc1")
	subnets := newSubnetRegistry(t)
	be := newFakeBackend()
	seedSubnet(t, subnets, "sn-1", "vpc-1", "10.0.1.0/24")
	rec := manager.NewSubnetReconciler(subnets, vpcs, be)
	if err := rec.Reconcile(context.Background(), "sn-1"); err != nil {
		t.Fatalf("reconcile up: %v", err)
	}

	cur, _ := subnets.Get("sn-1")
	now := time.Now()
	cur.Metadata.DeletionTimestamp = &now
	if err := subnets.Put(cur); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}
	if err := rec.Reconcile(context.Background(), "sn-1"); err != nil {
		t.Fatalf("reconcile delete: %v", err)
	}
	if _, err := subnets.Get("sn-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected record removed, got %v", err)
	}
	if be.addresses["br-vpc1 10.0.1.1/24"] {
		t.Fatal("gateway address should have been removed")
	}
	if rec.Name() != resource.KindSubnet {
		t.Fatalf("name = %q", rec.Name())
	}
}
