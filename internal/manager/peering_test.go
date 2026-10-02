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
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

type fakePeeringBackend struct {
	links         map[string][2]string // veth1 -> {bridge1, bridge2}
	deleted       []string
	routes        []string // "cidr1->cidr2" per EnsureRoutes
	routesDeleted []string
}

func newFakePeeringBackend() *fakePeeringBackend {
	return &fakePeeringBackend{links: make(map[string][2]string)}
}

func (f *fakePeeringBackend) EnsureLink(_ context.Context, veth1, _, bridge1, bridge2 string) error {
	f.links[veth1] = [2]string{bridge1, bridge2}
	return nil
}

func (f *fakePeeringBackend) EnsureRoutes(_ context.Context, a, b manager.PeeringSide) error {
	f.routes = append(f.routes, a.CIDR+"->"+b.CIDR)
	return nil
}

func (f *fakePeeringBackend) DeleteRoutes(_ context.Context, a, b manager.PeeringSide) error {
	f.routesDeleted = append(f.routesDeleted, a.CIDR+"->"+b.CIDR)
	return nil
}

func (f *fakePeeringBackend) DeleteLink(_ context.Context, veth1 string) error {
	delete(f.links, veth1)
	f.deleted = append(f.deleted, veth1)
	return nil
}

// peerRecorder records ip invocations and simulates "link show" as absent so
// EnsureLink proceeds to create the pair.
type peerRecorder struct {
	calls [][]string
}

func (r *peerRecorder) run(_ context.Context, name string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	if len(args) >= 2 && args[0] == "link" && args[1] == "show" {
		return "Device does not exist", errors.New("exit status 1")
	}
	return "", nil
}

func newPeeringRegistry(t *testing.T, store state.Store) *manager.PeeringRegistry {
	t.Helper()
	return registry.New[resource.PeeringSpec, resource.PeeringStatus](store, resource.KindPeering)
}

func seedPeering(t *testing.T, reg *manager.PeeringRegistry, uid, vpc1, vpc2 string) {
	t.Helper()
	if err := reg.Put(&resource.Peering{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.PeeringSpec{VPC1ID: vpc1, VPC2ID: vpc2},
	}); err != nil {
		t.Fatalf("seed peering: %v", err)
	}
}

func TestPeeringReconcileLinksBridges(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	vpcs := registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC)
	seedVPCWithBridge(t, vpcs, "vpc-1", "10.0.0.0/16", "br-vpc1")
	seedVPCWithBridge(t, vpcs, "vpc-2", "10.1.0.0/16", "br-vpc2")
	peerings := newPeeringRegistry(t, store)
	seedPeering(t, peerings, "pr-1", "vpc-1", "vpc-2")

	be := newFakePeeringBackend()
	rec := manager.NewPeeringReconciler(peerings, vpcs, be)
	if err := rec.Reconcile(context.Background(), "pr-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got, _ := peerings.Get("pr-1")
	if !got.Status.IsReady() {
		t.Fatalf("phase = %q, want Ready", got.Status.Phase)
	}
	if got.Status.Veth1 == "" || got.Status.Veth2 == "" {
		t.Fatalf("veth interfaces not recorded: %+v", got.Status)
	}
	if !got.Metadata.HasFinalizer(resource.PeeringFinalizer) {
		t.Fatal("finalizer should be attached")
	}
	link, ok := be.links[got.Status.Veth1]
	if !ok || link[0] != "br-vpc1" || link[1] != "br-vpc2" {
		t.Fatalf("link not created across both bridges: %+v", be.links)
	}
	if rec.Name() != resource.KindPeering {
		t.Fatalf("name = %q", rec.Name())
	}
}

func TestPeeringPendingWhenBridgeMissing(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	vpcs := registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC)
	seedVPCWithBridge(t, vpcs, "vpc-1", "10.0.0.0/16", "br-vpc1")
	// vpc-2 exists but has no bridge yet.
	if err := vpcs.Put(&resource.VPC{Metadata: resource.ObjectMeta{UID: "vpc-2", Generation: 1}, Spec: resource.VPCSpec{CIDR: "10.1.0.0/16"}}); err != nil {
		t.Fatalf("seed vpc-2: %v", err)
	}
	peerings := newPeeringRegistry(t, store)
	seedPeering(t, peerings, "pr-1", "vpc-1", "vpc-2")

	be := newFakePeeringBackend()
	rec := manager.NewPeeringReconciler(peerings, vpcs, be)
	if err := rec.Reconcile(context.Background(), "pr-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _ := peerings.Get("pr-1")
	if got.Status.Phase != resource.PhasePending {
		t.Fatalf("phase = %q, want Pending", got.Status.Phase)
	}
	if len(be.links) != 0 {
		t.Fatalf("no link should be created while pending: %+v", be.links)
	}
}

func TestPeeringVPCNotFound(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	vpcs := registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC)
	seedVPCWithBridge(t, vpcs, "vpc-1", "10.0.0.0/16", "br-vpc1")
	peerings := newPeeringRegistry(t, store)
	seedPeering(t, peerings, "pr-1", "vpc-1", "missing")

	rec := manager.NewPeeringReconciler(peerings, vpcs, newFakePeeringBackend())
	if err := rec.Reconcile(context.Background(), "pr-1"); err == nil {
		t.Fatal("expected an error when a VPC is missing")
	}
	got, _ := peerings.Get("pr-1")
	if got.Status.Phase != resource.PhaseError {
		t.Fatalf("phase = %q, want Error", got.Status.Phase)
	}
}

func TestPeeringFinalize(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	vpcs := registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC)
	seedVPCWithBridge(t, vpcs, "vpc-1", "10.0.0.0/16", "br-vpc1")
	seedVPCWithBridge(t, vpcs, "vpc-2", "10.1.0.0/16", "br-vpc2")
	peerings := newPeeringRegistry(t, store)
	seedPeering(t, peerings, "pr-1", "vpc-1", "vpc-2")

	be := newFakePeeringBackend()
	rec := manager.NewPeeringReconciler(peerings, vpcs, be)
	if err := rec.Reconcile(context.Background(), "pr-1"); err != nil {
		t.Fatalf("reconcile up: %v", err)
	}

	cur, _ := peerings.Get("pr-1")
	now := time.Now()
	cur.Metadata.DeletionTimestamp = &now
	if err := peerings.Put(cur); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}
	if err := rec.Reconcile(context.Background(), "pr-1"); err != nil {
		t.Fatalf("reconcile delete: %v", err)
	}
	if _, err := peerings.Get("pr-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("record should be removed, got %v", err)
	}
	if len(be.deleted) != 1 {
		t.Fatalf("link should have been deleted once, got %d", len(be.deleted))
	}
}

func TestExecPeeringCommands(t *testing.T) {
	t.Parallel()

	rec := &peerRecorder{}
	be := manager.NewExecPeeringWithRunner(rec.run)
	ctx := context.Background()

	if err := be.EnsureLink(ctx, "pa-1", "pb-1", "br-vpc1", "br-vpc2"); err != nil {
		t.Fatalf("ensure link: %v", err)
	}
	for _, want := range []string{"veth", "br-vpc1", "br-vpc2"} {
		if !anyCallHas(rec.calls, want) {
			t.Fatalf("missing %q in calls: %v", want, rec.calls)
		}
	}

	rec.calls = nil
	if err := be.DeleteLink(ctx, "pa-1"); err != nil {
		t.Fatalf("delete link: %v", err)
	}
	if !anyCallHas(rec.calls, "del") {
		t.Fatalf("expected link deletion: %v", rec.calls)
	}
}

func TestPeeringRoutesEachVPCFromTheOther(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	vpcs := registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC)
	for uid, cidr := range map[string]string{"vpc-a": "10.1.0.0/16", "vpc-b": "10.2.0.0/16", "vpc-c": "10.1.128.0/17"} {
		v := &resource.VPC{Metadata: resource.ObjectMeta{UID: uid, Generation: 1}, Spec: resource.VPCSpec{CIDR: cidr}}
		v.Status.BridgeName = "br-" + uid
		if err := vpcs.Put(v); err != nil {
			t.Fatal(err)
		}
	}
	peerings := newPeeringRegistry(t, store)
	seedPeering(t, peerings, "p-ab", "vpc-a", "vpc-b")
	seedPeering(t, peerings, "p-ac", "vpc-a", "vpc-c")
	be := newFakePeeringBackend()
	r := manager.NewPeeringReconciler(peerings, vpcs, be)

	if err := r.Reconcile(context.Background(), "p-ab"); err != nil {
		t.Fatalf("p-ab: %v", err)
	}
	if !slices.Equal(be.routes, []string{"10.1.0.0/16->10.2.0.0/16"}) {
		t.Fatalf("routes %v", be.routes)
	}
	if err := r.Reconcile(context.Background(), "p-ac"); err == nil {
		t.Fatal("overlapping VPCs must not be peered")
	}
	if got, _ := peerings.Get("p-ac"); got.Status.Phase != resource.PhaseError {
		t.Fatalf("p-ac status %+v", got.Status)
	}
}

func TestExecPeeringRoutesInBothTables(t *testing.T) {
	t.Parallel()

	rec := &peerRecorder{}
	a := manager.PeeringSide{VPCID: "vpc-a", CIDR: "10.1.0.0/16", Bridge: "br-a"}
	b := manager.PeeringSide{VPCID: "vpc-b", CIDR: "10.2.0.0/16", Bridge: "br-b"}
	if err := manager.NewExecPeeringWithRunner(rec.run).EnsureRoutes(context.Background(), a, b); err != nil {
		t.Fatal(err)
	}
	var routes [][]string
	for _, c := range rec.calls {
		if strings.HasPrefix(strings.Join(c, " "), "ip route replace") {
			routes = append(routes, c)
		}
	}
	// Each CIDR goes in the other VPC's table, onto its own bridge.
	if len(routes) != 2 ||
		strings.Join(routes[0][3:6], " ") != "10.2.0.0/16 dev br-b" ||
		strings.Join(routes[1][3:6], " ") != "10.1.0.0/16 dev br-a" ||
		routes[0][7] == routes[1][7] {
		t.Fatalf("routes %v", routes)
	}
}
