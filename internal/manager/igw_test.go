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

func newIGWRegistry(t *testing.T) *manager.IGWRegistry {
	t.Helper()
	return registry.New[resource.IGWSpec, resource.IGWStatus](state.NewFileStore(t.TempDir()), resource.KindIGW)
}

func TestIGWReconcileConfiguresNAT(t *testing.T) {
	t.Parallel()

	vpcs := newVPCRegistry(t)
	seedVPCWithBridge(t, vpcs, "vpc-1", "10.0.0.0/16", "br-vpc1")
	igws := newIGWRegistry(t)
	be := newFakeBackend()
	if err := igws.Put(&resource.IGW{Metadata: resource.ObjectMeta{UID: "igw-1", Generation: 1}, Spec: resource.IGWSpec{VPCID: "vpc-1"}}); err != nil {
		t.Fatalf("seed igw: %v", err)
	}

	rec := manager.NewIGWReconciler(igws, vpcs, be)
	if err := rec.Reconcile(context.Background(), "igw-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _ := igws.Get("igw-1")
	if !got.Status.IsReady() {
		t.Fatalf("phase = %q, want Ready", got.Status.Phase)
	}
	if !be.forwarding {
		t.Fatal("forwarding should be enabled")
	}
	if !be.nat["10.0.0.0/16 eth0"] {
		t.Fatalf("nat rule not added: %v", be.nat)
	}
	if got.Status.HostIface != "eth0" {
		t.Fatalf("hostIface = %q", got.Status.HostIface)
	}
}

func TestIGWPendingWhenVPCBridgeMissing(t *testing.T) {
	t.Parallel()

	vpcs := newVPCRegistry(t)
	if err := vpcs.Put(&resource.VPC{Metadata: resource.ObjectMeta{UID: "vpc-1"}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}); err != nil {
		t.Fatalf("seed vpc: %v", err)
	}
	igws := newIGWRegistry(t)
	if err := igws.Put(&resource.IGW{Metadata: resource.ObjectMeta{UID: "igw-1"}, Spec: resource.IGWSpec{VPCID: "vpc-1"}}); err != nil {
		t.Fatalf("seed igw: %v", err)
	}
	rec := manager.NewIGWReconciler(igws, vpcs, newFakeBackend())
	if err := rec.Reconcile(context.Background(), "igw-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _ := igws.Get("igw-1")
	if got.Status.Phase != resource.PhasePending {
		t.Fatalf("phase = %q, want Pending", got.Status.Phase)
	}
}

func TestIGWFinalize(t *testing.T) {
	t.Parallel()

	vpcs := newVPCRegistry(t)
	seedVPCWithBridge(t, vpcs, "vpc-1", "10.0.0.0/16", "br-vpc1")
	igws := newIGWRegistry(t)
	be := newFakeBackend()
	if err := igws.Put(&resource.IGW{Metadata: resource.ObjectMeta{UID: "igw-1", Generation: 1}, Spec: resource.IGWSpec{VPCID: "vpc-1"}}); err != nil {
		t.Fatalf("seed igw: %v", err)
	}
	rec := manager.NewIGWReconciler(igws, vpcs, be)
	if err := rec.Reconcile(context.Background(), "igw-1"); err != nil {
		t.Fatalf("reconcile up: %v", err)
	}

	cur, _ := igws.Get("igw-1")
	now := time.Now()
	cur.Metadata.DeletionTimestamp = &now
	if err := igws.Put(cur); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}
	if err := rec.Reconcile(context.Background(), "igw-1"); err != nil {
		t.Fatalf("reconcile delete: %v", err)
	}
	if _, err := igws.Get("igw-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected record removed, got %v", err)
	}
	if be.nat["10.0.0.0/16 eth0"] {
		t.Fatal("nat rule should have been removed")
	}
}
