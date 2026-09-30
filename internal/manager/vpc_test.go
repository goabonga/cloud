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

// fakeBackend is an in-memory NetworkBackend for tests.
type fakeBackend struct {
	bridges     map[string]bool
	addresses   map[string]bool // "iface addr/prefix"
	nat         map[string]bool // "cidr iface"
	forwarding  bool
	defaultIfc  string
	ensureErr   error
	deleteErr   error
	addrErr     error
	natErr      error
	ensureCalls []string
	deleteCalls []string
	nodePorts   map[string]string // port -> "bridge peer"
	gateways    map[string]bool   // "bridge addr/prefix" assigned with noprefixroute
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		bridges:    make(map[string]bool),
		addresses:  make(map[string]bool),
		nat:        make(map[string]bool),
		defaultIfc: "eth0",
	}
}

func (f *fakeBackend) EnsureAddress(_ context.Context, iface, addrCIDR string) error {
	if f.addrErr != nil {
		return f.addrErr
	}
	f.addresses[iface+" "+addrCIDR] = true
	return nil
}

func (f *fakeBackend) DeleteAddress(_ context.Context, iface, addrCIDR string) error {
	delete(f.addresses, iface+" "+addrCIDR)
	return nil
}

func (f *fakeBackend) EnableForwarding(_ context.Context) error {
	f.forwarding = true
	return nil
}

func (f *fakeBackend) EnsureNAT(_ context.Context, sourceCIDR, hostIface string) error {
	if f.natErr != nil {
		return f.natErr
	}
	f.nat[sourceCIDR+" "+hostIface] = true
	return nil
}

func (f *fakeBackend) DeleteNAT(_ context.Context, sourceCIDR, hostIface string) error {
	delete(f.nat, sourceCIDR+" "+hostIface)
	return nil
}

func (f *fakeBackend) DefaultInterface(_ context.Context) (string, error) {
	return f.defaultIfc, nil
}

func (f *fakeBackend) EnsureBridge(_ context.Context, b manager.Bridge) error {
	if f.ensureErr != nil {
		return f.ensureErr
	}
	f.bridges[b.Name] = true
	f.ensureCalls = append(f.ensureCalls, b.Name)
	return nil
}

func (f *fakeBackend) DeleteBridge(_ context.Context, name string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.bridges, name)
	f.deleteCalls = append(f.deleteCalls, name)
	return nil
}

func (f *fakeBackend) BridgeExists(_ context.Context, name string) (bool, error) {
	return f.bridges[name], nil
}

func (f *fakeBackend) EnsureNodePort(_ context.Context, bridge, port, peer string) error {
	if f.nodePorts == nil {
		f.nodePorts = map[string]string{}
	}
	f.nodePorts[port] = bridge + " " + peer
	return nil
}

func (f *fakeBackend) DeleteNodePort(_ context.Context, port string) error {
	delete(f.nodePorts, port)
	return nil
}

// EnsureGatewayAddress records the gateway like EnsureAddress; gateways
// records which addresses were assigned without a prefix route.
func (f *fakeBackend) EnsureGatewayAddress(ctx context.Context, bridge, addrCIDR string) error {
	if f.gateways == nil {
		f.gateways = map[string]bool{}
	}
	f.gateways[bridge+" "+addrCIDR] = true
	return f.EnsureAddress(ctx, bridge, addrCIDR)
}

func newVPCRegistry(t *testing.T) *manager.VPCRegistry {
	t.Helper()
	return registry.New[resource.VPCSpec, resource.VPCStatus](state.NewFileStore(t.TempDir()), resource.KindVPC)
}

func TestVPCReconcileEnsuresBridge(t *testing.T) {
	t.Parallel()

	reg := newVPCRegistry(t)
	be := newFakeBackend()
	rec := manager.NewVPCReconciler(reg, be)

	vpc := &resource.VPC{
		Metadata: resource.ObjectMeta{UID: "vpc-1", Generation: 1},
		Spec:     resource.VPCSpec{CIDR: "10.0.0.0/16"},
	}
	if err := reg.Put(vpc); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := rec.Reconcile(context.Background(), "vpc-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got, err := reg.Get("vpc-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Status.IsReady() {
		t.Fatalf("phase = %q, want Ready", got.Status.Phase)
	}
	if got.Status.BridgeName == "" || !be.bridges[got.Status.BridgeName] {
		t.Fatalf("bridge not ensured: status=%q backend=%v", got.Status.BridgeName, be.bridges)
	}
	if !got.Metadata.HasFinalizer(resource.VPCFinalizer) {
		t.Fatal("finalizer should be attached")
	}
	if got.Status.ObservedGeneration != 1 {
		t.Fatalf("observedGeneration = %d, want 1", got.Status.ObservedGeneration)
	}

	// Idempotent: a second pass keeps it Ready.
	if err := rec.Reconcile(context.Background(), "vpc-1"); err != nil {
		t.Fatalf("reconcile again: %v", err)
	}
}

func TestVPCReconcileMissingIsNoop(t *testing.T) {
	t.Parallel()

	rec := manager.NewVPCReconciler(newVPCRegistry(t), newFakeBackend())
	if err := rec.Reconcile(context.Background(), "ghost"); err != nil {
		t.Fatalf("expected no error for missing vpc, got %v", err)
	}
}

func TestVPCReconcileBridgeErrorSetsErrorPhase(t *testing.T) {
	t.Parallel()

	reg := newVPCRegistry(t)
	be := newFakeBackend()
	be.ensureErr = errors.New("boom")
	rec := manager.NewVPCReconciler(reg, be)

	vpc := &resource.VPC{Metadata: resource.ObjectMeta{UID: "vpc-1", Generation: 1}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	if err := reg.Put(vpc); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := rec.Reconcile(context.Background(), "vpc-1"); err == nil {
		t.Fatal("expected reconcile error")
	}
	got, _ := reg.Get("vpc-1")
	if got.Status.Phase != resource.PhaseError {
		t.Fatalf("phase = %q, want Error", got.Status.Phase)
	}
}

func TestVPCReconcileFinalizes(t *testing.T) {
	t.Parallel()

	reg := newVPCRegistry(t)
	be := newFakeBackend()
	rec := manager.NewVPCReconciler(reg, be)

	// Bring it up first so a bridge and finalizer exist.
	vpc := &resource.VPC{Metadata: resource.ObjectMeta{UID: "vpc-1", Generation: 1}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	if err := reg.Put(vpc); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := rec.Reconcile(context.Background(), "vpc-1"); err != nil {
		t.Fatalf("reconcile up: %v", err)
	}

	// Request deletion (what the API soft-delete does).
	cur, _ := reg.Get("vpc-1")
	now := time.Now()
	cur.Metadata.DeletionTimestamp = &now
	if err := reg.Put(cur); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}

	if err := rec.Reconcile(context.Background(), "vpc-1"); err != nil {
		t.Fatalf("reconcile delete: %v", err)
	}
	if _, err := reg.Get("vpc-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected record removed, got %v", err)
	}
	if len(be.deleteCalls) != 1 {
		t.Fatalf("expected one bridge delete, got %v", be.deleteCalls)
	}
}
