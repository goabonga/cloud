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

type fakeFirewall struct {
	applied  map[string][]resource.ACLRule
	cleared  []string
	applyErr error
}

func newFakeFirewall() *fakeFirewall {
	return &fakeFirewall{applied: make(map[string][]resource.ACLRule)}
}

func (f *fakeFirewall) Apply(_ context.Context, chain string, rules []resource.ACLRule) error {
	if f.applyErr != nil {
		return f.applyErr
	}
	f.applied[chain] = rules
	return nil
}

func (f *fakeFirewall) Clear(_ context.Context, chain string) error {
	delete(f.applied, chain)
	f.cleared = append(f.cleared, chain)
	return nil
}

func newACLRegistry(t *testing.T) *manager.ACLRegistry {
	t.Helper()
	return registry.New[resource.ACLPolicySpec, resource.ACLPolicyStatus](state.NewFileStore(t.TempDir()), resource.KindACLPolicy)
}

func seedPolicy(t *testing.T, reg *manager.ACLRegistry, uid string) {
	t.Helper()
	pol := &resource.ACLPolicy{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.ACLPolicySpec{Rules: []resource.ACLRule{{Action: "allow", Protocol: "tcp", Port: 443}}},
	}
	if err := reg.Put(pol); err != nil {
		t.Fatalf("seed %s: %v", uid, err)
	}
}

func TestACLReconcileApplies(t *testing.T) {
	t.Parallel()

	reg := newACLRegistry(t)
	fw := newFakeFirewall()
	rec := manager.NewACLReconciler(reg, fw)
	seedPolicy(t, reg, "acl-1")

	if err := rec.Reconcile(context.Background(), "acl-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _ := reg.Get("acl-1")
	if !got.Status.IsReady() {
		t.Fatalf("phase = %q, want Ready", got.Status.Phase)
	}
	if got.Status.Chain == "" || got.Status.AppliedRules != 1 {
		t.Fatalf("unexpected status: %+v", got.Status)
	}
	if !got.Metadata.HasFinalizer(resource.ACLFinalizer) {
		t.Fatal("finalizer should be attached")
	}
	if _, ok := fw.applied[got.Status.Chain]; !ok {
		t.Fatalf("rules not applied to chain %q", got.Status.Chain)
	}
}

func TestACLReconcileApplyErrorSetsErrorPhase(t *testing.T) {
	t.Parallel()

	reg := newACLRegistry(t)
	fw := newFakeFirewall()
	fw.applyErr = errors.New("boom")
	rec := manager.NewACLReconciler(reg, fw)
	seedPolicy(t, reg, "acl-1")

	if err := rec.Reconcile(context.Background(), "acl-1"); err == nil {
		t.Fatal("expected reconcile error")
	}
	got, _ := reg.Get("acl-1")
	if got.Status.Phase != resource.PhaseError {
		t.Fatalf("phase = %q, want Error", got.Status.Phase)
	}
}

func TestACLReconcileFinalizes(t *testing.T) {
	t.Parallel()

	reg := newACLRegistry(t)
	fw := newFakeFirewall()
	rec := manager.NewACLReconciler(reg, fw)
	seedPolicy(t, reg, "acl-1")
	if err := rec.Reconcile(context.Background(), "acl-1"); err != nil {
		t.Fatalf("reconcile up: %v", err)
	}

	cur, _ := reg.Get("acl-1")
	now := time.Now()
	cur.Metadata.DeletionTimestamp = &now
	if err := reg.Put(cur); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}
	if err := rec.Reconcile(context.Background(), "acl-1"); err != nil {
		t.Fatalf("reconcile delete: %v", err)
	}
	if _, err := reg.Get("acl-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected record removed, got %v", err)
	}
	if len(fw.cleared) != 1 {
		t.Fatalf("expected one chain cleared, got %v", fw.cleared)
	}
}

func TestACLReconcileAll(t *testing.T) {
	t.Parallel()

	reg := newACLRegistry(t)
	fw := newFakeFirewall()
	rec := manager.NewACLReconciler(reg, fw)
	seedPolicy(t, reg, "acl-1")
	seedPolicy(t, reg, "acl-2")

	if err := rec.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("reconcile all: %v", err)
	}
	if rec.Name() != resource.KindACLPolicy {
		t.Fatalf("name = %q", rec.Name())
	}
	if len(fw.applied) != 2 {
		t.Fatalf("expected 2 chains applied, got %d", len(fw.applied))
	}
}
