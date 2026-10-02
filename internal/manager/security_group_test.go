// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

type fakeSGBackend struct {
	chains map[string][]resource.SecurityGroupRuleSpec
}

func newFakeSGBackend() *fakeSGBackend {
	return &fakeSGBackend{chains: make(map[string][]resource.SecurityGroupRuleSpec)}
}

func (f *fakeSGBackend) EnsureChain(_ context.Context, chain string, rules []resource.SecurityGroupRuleSpec) error {
	f.chains[chain] = rules
	return nil
}

func (f *fakeSGBackend) DeleteChain(_ context.Context, chain string) error {
	delete(f.chains, chain)
	return nil
}

func newSGRegistry(t *testing.T, store state.Store) *manager.SecurityGroupRegistry {
	t.Helper()
	return registry.New[resource.SecurityGroupSpec, resource.SecurityGroupStatus](store, resource.KindSecurityGroup)
}

func newSGRuleRegistry(t *testing.T, store state.Store) *manager.SecurityGroupRuleRegistry {
	t.Helper()
	return registry.New[resource.SecurityGroupRuleSpec, resource.SecurityGroupRuleStatus](store, resource.KindSecurityGroupRule)
}

func seedSG(t *testing.T, reg *manager.SecurityGroupRegistry, uid string) {
	t.Helper()
	if err := reg.Put(&resource.SecurityGroup{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.SecurityGroupSpec{VPCID: "vpc-1"},
	}); err != nil {
		t.Fatalf("seed sg: %v", err)
	}
}

func seedSGRule(t *testing.T, reg *manager.SecurityGroupRuleRegistry, uid, sgID string, port int) {
	t.Helper()
	if err := reg.Put(&resource.SecurityGroupRule{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.SecurityGroupRuleSpec{SecurityGroupID: sgID, Direction: "ingress", Protocol: "tcp", Port: port, CIDR: "0.0.0.0/0"},
	}); err != nil {
		t.Fatalf("seed sg rule: %v", err)
	}
}

func TestSecurityGroupReconcileBuildsChain(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	sgs := newSGRegistry(t, store)
	rules := newSGRuleRegistry(t, store)
	seedSG(t, sgs, "sg-1")
	seedSGRule(t, rules, "r-1", "sg-1", 80)
	seedSGRule(t, rules, "r-2", "sg-1", 443)
	seedSGRule(t, rules, "r-other", "sg-2", 22) // belongs to a different group

	be := newFakeSGBackend()
	rec := manager.NewSecurityGroupReconciler(sgs, rules, be)
	if err := rec.Reconcile(context.Background(), "sg-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got, _ := sgs.Get("sg-1")
	if !got.Status.IsReady() {
		t.Fatalf("phase = %q, want Ready", got.Status.Phase)
	}
	if !strings.HasPrefix(got.Status.Chain, "INFRA-SG-") {
		t.Fatalf("chain = %q, want INFRA-SG- prefix", got.Status.Chain)
	}
	if !got.Metadata.HasFinalizer(resource.SecurityGroupFinalizer) {
		t.Fatal("finalizer should be attached")
	}
	if n := len(be.chains[got.Status.Chain]); n != 2 {
		t.Fatalf("chain has %d rules, want 2 (only sg-1's)", n)
	}
	if rec.Name() != resource.KindSecurityGroup {
		t.Fatalf("name = %q", rec.Name())
	}
}

func TestSecurityGroupFinalize(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	sgs := newSGRegistry(t, store)
	rules := newSGRuleRegistry(t, store)
	seedSG(t, sgs, "sg-1")
	be := newFakeSGBackend()
	rec := manager.NewSecurityGroupReconciler(sgs, rules, be)
	if err := rec.Reconcile(context.Background(), "sg-1"); err != nil {
		t.Fatalf("reconcile up: %v", err)
	}
	chain, _ := sgs.Get("sg-1")
	if _, ok := be.chains[chain.Status.Chain]; !ok {
		t.Fatal("chain should exist before delete")
	}

	now := time.Now()
	chain.Metadata.DeletionTimestamp = &now
	if err := sgs.Put(chain); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}
	if err := rec.Reconcile(context.Background(), "sg-1"); err != nil {
		t.Fatalf("reconcile delete: %v", err)
	}
	if _, err := sgs.Get("sg-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("record should be removed, got %v", err)
	}
	if len(be.chains) != 0 {
		t.Fatalf("chain should be removed from backend: %v", be.chains)
	}
}

func TestExecSecurityGroupEnsureChain(t *testing.T) {
	t.Parallel()

	rec := &fwRecorder{}
	be := manager.NewExecSecurityGroupWithRunner(rec.run)
	rules := []resource.SecurityGroupRuleSpec{
		{SecurityGroupID: "sg-1", Direction: "ingress", Protocol: "tcp", Port: 80, CIDR: "10.0.0.0/8"},
	}
	if err := be.EnsureChain(context.Background(), "INFRA-SG-X", rules); err != nil {
		t.Fatalf("ensure chain: %v", err)
	}
	for _, want := range []string{"-N", "ESTABLISHED,RELATED", "ACCEPT", "DROP"} {
		if !anyCallHas(rec.calls, want) {
			t.Fatalf("missing %q in calls: %v", want, rec.calls)
		}
	}
}
