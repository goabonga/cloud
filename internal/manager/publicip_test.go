// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func newPublicIPEnv(t *testing.T) (*manager.IPAddressRegistry, state.Store) {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	return registry.New[resource.IPAddressSpec, resource.IPAddressStatus](store, resource.KindIPAddress), store
}

func putPublicIP(t *testing.T, reg *manager.IPAddressRegistry, uid, addr string) {
	t.Helper()
	if err := reg.Put(&resource.IPAddress{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.IPAddressSpec{Type: "public", Address: addr},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPublicIPIsANoopOffTheEdges(t *testing.T) {
	t.Parallel()

	reg, store := newPublicIPEnv(t)
	putPublicIP(t, reg, "ip-1", "")
	if err := manager.NewPublicIPReconciler(reg, store, "").ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := reg.Get("ip-1"); got.Status.Address != "" || got.Status.Phase != "" {
		t.Fatalf("a host without a public block must leave it alone: %+v", got.Status)
	}
}

func TestPublicIPResolvesPinnedAndAllocatedAddresses(t *testing.T) {
	t.Parallel()

	reg, store := newPublicIPEnv(t)
	putPublicIP(t, reg, "pinned", "203.0.113.10")
	putPublicIP(t, reg, "auto-1", "")
	putPublicIP(t, reg, "auto-2", "")
	putPublicIP(t, reg, "outside", "198.51.100.7")
	putPublicIP(t, reg, "reserved", "203.0.113.53")
	err := manager.NewPublicIPReconciler(reg, store, "203.0.113.0/24", "203.0.113.53").ReconcileAll(context.Background())
	if err == nil {
		t.Fatal("expected errors for the outside and reserved addresses")
	}
	pinned, _ := reg.Get("pinned")
	a1, _ := reg.Get("auto-1")
	a2, _ := reg.Get("auto-2")
	if pinned.Status.Address != "203.0.113.10" || !pinned.Status.IsConverged(1) {
		t.Fatalf("pinned: %+v", pinned.Status)
	}
	if a1.Status.Address == "" || a2.Status.Address == "" || a1.Status.Address == a2.Status.Address || a1.Status.Address == "203.0.113.10" {
		t.Fatalf("allocated %q and %q next to the pinned 203.0.113.10", a1.Status.Address, a2.Status.Address)
	}
	for _, uid := range []string{"outside", "reserved"} {
		if got, _ := reg.Get(uid); got.Status.Phase != resource.PhaseError {
			t.Fatalf("%s: phase %q, want Error", uid, got.Status.Phase)
		}
	}
}

func TestPublicIPEdgesReconcilingAtOnceAgree(t *testing.T) {
	t.Parallel()

	reg, store := newPublicIPEnv(t)
	putPublicIP(t, reg, "lb", "")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ { // four edges sharing the store
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = manager.NewPublicIPReconciler(reg, store, "203.0.113.0/24").ReconcileAll(context.Background())
		}()
	}
	wg.Wait()
	got, _ := reg.Get("lb")
	kvs, err := store.List("ipam/public")
	if err != nil || len(kvs) != 1 || kvs[0].Key != "ipam/public/"+got.Status.Address {
		t.Fatalf("reservations %v (err %v) for address %q, want exactly one", kvs, err, got.Status.Address)
	}
}

func TestPublicIPRejectsAnAddressAnotherHolds(t *testing.T) {
	t.Parallel()

	reg, store := newPublicIPEnv(t)
	putPublicIP(t, reg, "first", "203.0.113.10")
	r := manager.NewPublicIPReconciler(reg, store, "203.0.113.0/24")
	if err := r.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	putPublicIP(t, reg, "second", "203.0.113.10")
	if err := r.ReconcileAll(context.Background()); err == nil {
		t.Fatal("expected a conflict")
	}
	if got, _ := reg.Get("second"); got.Status.Phase != resource.PhaseError {
		t.Fatalf("second: %+v", got.Status)
	}
}

func TestPublicIPReleasesTheReservationOfADeletedAddress(t *testing.T) {
	t.Parallel()

	reg, store := newPublicIPEnv(t)
	putPublicIP(t, reg, "gone", "203.0.113.10")
	r := manager.NewPublicIPReconciler(reg, store, "203.0.113.0/24")
	if err := r.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := reg.Delete("gone"); err != nil {
		t.Fatal(err)
	}
	if err := r.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get("ipam/public/203.0.113.10"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("reservation kept: %v", err)
	}
}
