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

type fakeWAFBackend struct {
	chain   string
	match   []string
	rules   []resource.WAFRuleSpec
	log     bool
	deleted bool
}

func (f *fakeWAFBackend) EnsureChain(_ context.Context, chain string, match []string, rules []resource.WAFRuleSpec, logEnabled bool) error {
	f.chain, f.match, f.rules, f.log = chain, match, rules, logEnabled
	return nil
}

func (f *fakeWAFBackend) DeleteChain(_ context.Context, _ string, _ []string) error {
	f.deleted = true
	return nil
}

type wafEnv struct {
	policies *manager.WAFPolicyRegistry
	rules    *manager.WAFRuleRegistry
	computes *manager.ComputeRegistry
	subnets  *manager.SubnetRegistry
	igws     *manager.IGWRegistry
	vpcs     *manager.VPCRegistry
}

func newWAFEnv(t *testing.T) *wafEnv {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	return &wafEnv{
		policies: registry.New[resource.WAFPolicySpec, resource.WAFPolicyStatus](store, resource.KindWAFPolicy),
		rules:    registry.New[resource.WAFRuleSpec, resource.WAFRuleStatus](store, resource.KindWAFRule),
		computes: registry.New[resource.ComputeSpec, resource.ComputeStatus](store, resource.KindCompute),
		subnets:  registry.New[resource.SubnetSpec, resource.SubnetStatus](store, resource.KindSubnet),
		igws:     registry.New[resource.IGWSpec, resource.IGWStatus](store, resource.KindIGW),
		vpcs:     registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC),
	}
}

// putNetwork seeds subnet sn-1 (10.0.1.0/24) in vpc-1, on bridge br-vpc1.
func (env *wafEnv) putNetwork(t *testing.T) {
	t.Helper()
	if err := env.subnets.Put(&resource.Subnet{Metadata: resource.ObjectMeta{UID: "sn-1", Generation: 1}, Spec: resource.SubnetSpec{VPCID: "vpc-1", CIDR: "10.0.1.0/24"}}); err != nil {
		t.Fatalf("seed subnet: %v", err)
	}
	v := &resource.VPC{Metadata: resource.ObjectMeta{UID: "vpc-1", Generation: 1}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	v.Status.BridgeName = "br-vpc1"
	if err := env.vpcs.Put(v); err != nil {
		t.Fatalf("seed vpc: %v", err)
	}
}

func (env *wafEnv) reconciler(be manager.WAFBackend) *manager.WAFReconciler {
	return manager.NewWAFReconciler(env.policies, env.rules, env.computes, env.subnets, env.igws, env.vpcs, be)
}

func (env *wafEnv) putPolicy(t *testing.T, uid, targetType, targetID string, log bool) {
	t.Helper()
	if err := env.policies.Put(&resource.WAFPolicy{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.WAFPolicySpec{TargetType: targetType, TargetID: targetID, LogEnabled: log},
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
}

func TestWAFReconcileBuildsChainForCompute(t *testing.T) {
	t.Parallel()

	env := newWAFEnv(t)
	env.putNetwork(t)
	c := &resource.Compute{Metadata: resource.ObjectMeta{UID: "i-1", Generation: 1}, Spec: resource.ComputeSpec{SubnetID: "sn-1", Image: "x"}}
	c.Status.IP = "10.0.1.10"
	if err := env.computes.Put(c); err != nil {
		t.Fatalf("seed compute: %v", err)
	}
	env.putPolicy(t, "waf-1", "compute", "i-1", true)
	if err := env.rules.Put(&resource.WAFRule{Metadata: resource.ObjectMeta{UID: "wr-1", Generation: 1}, Spec: resource.WAFRuleSpec{PolicyID: "waf-1", Priority: 10, MatchType: "source_ip", MatchValue: "1.2.3.0/24", Action: "block"}}); err != nil {
		t.Fatalf("seed rule: %v", err)
	}
	if err := env.rules.Put(&resource.WAFRule{Metadata: resource.ObjectMeta{UID: "wr-other", Generation: 1}, Spec: resource.WAFRuleSpec{PolicyID: "waf-2", MatchType: "ip", Action: "block"}}); err != nil {
		t.Fatalf("seed other rule: %v", err)
	}

	be := &fakeWAFBackend{}
	rec := env.reconciler(be)
	if err := rec.Reconcile(context.Background(), "waf-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got, _ := env.policies.Get("waf-1")
	if !got.Status.IsReady() || !strings.HasPrefix(got.Status.Chain, "INFRA-WAF-") {
		t.Fatalf("unexpected status: %+v", got.Status)
	}
	if !got.Metadata.HasFinalizer(resource.WAFPolicyFinalizer) {
		t.Fatal("finalizer should be attached")
	}
	if strings.Join(be.match, " ") != "-o br-vpc1 -d 10.0.1.10" {
		t.Fatalf("match = %v, want [-o br-vpc1 -d 10.0.1.10]", be.match)
	}
	if len(be.rules) != 1 {
		t.Fatalf("chain got %d rules, want 1 (only waf-1's)", len(be.rules))
	}
	if !be.log {
		t.Fatal("logEnabled should be propagated")
	}
	if rec.Name() != resource.KindWAFPolicy {
		t.Fatalf("name = %q", rec.Name())
	}
}

func TestWAFReconcileSubnetAndIGWTargets(t *testing.T) {
	t.Parallel()

	env := newWAFEnv(t)
	sn := &resource.Subnet{Metadata: resource.ObjectMeta{UID: "sn-1", Generation: 1}, Spec: resource.SubnetSpec{VPCID: "vpc-1", CIDR: "10.0.1.0/24"}}
	if err := env.subnets.Put(sn); err != nil {
		t.Fatalf("seed subnet: %v", err)
	}
	v := &resource.VPC{Metadata: resource.ObjectMeta{UID: "vpc-1", Generation: 1}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	v.Status.BridgeName = "br-vpc1"
	if err := env.vpcs.Put(v); err != nil {
		t.Fatalf("seed vpc: %v", err)
	}
	if err := env.igws.Put(&resource.IGW{Metadata: resource.ObjectMeta{UID: "igw-1", Generation: 1}, Spec: resource.IGWSpec{VPCID: "vpc-1"}}); err != nil {
		t.Fatalf("seed igw: %v", err)
	}

	env.putPolicy(t, "waf-sn", "subnet", "sn-1", false)
	env.putPolicy(t, "waf-gw", "igw", "igw-1", false)
	rec := env.reconciler(&fakeWAFBackend{})

	beSn := &fakeWAFBackend{}
	if err := env.reconciler(beSn).Reconcile(context.Background(), "waf-sn"); err != nil {
		t.Fatalf("reconcile subnet waf: %v", err)
	}
	if strings.Join(beSn.match, " ") != "-o br-vpc1 -d 10.0.1.0/24" {
		t.Fatalf("subnet match = %v", beSn.match)
	}

	beGw := &fakeWAFBackend{}
	if err := env.reconciler(beGw).Reconcile(context.Background(), "waf-gw"); err != nil {
		t.Fatalf("reconcile igw waf: %v", err)
	}
	if len(beGw.match) != 2 || beGw.match[0] != "-i" || beGw.match[1] != "br-vpc1" {
		t.Fatalf("igw match = %v, want [-i br-vpc1]", beGw.match)
	}
	_ = rec
}

func TestWAFPendingWhenTargetNotReady(t *testing.T) {
	t.Parallel()

	env := newWAFEnv(t)
	// Compute exists but has no IP yet.
	if err := env.computes.Put(&resource.Compute{Metadata: resource.ObjectMeta{UID: "i-1", Generation: 1}, Spec: resource.ComputeSpec{SubnetID: "sn-1", Image: "x"}}); err != nil {
		t.Fatalf("seed compute: %v", err)
	}
	env.putPolicy(t, "waf-1", "compute", "i-1", false)
	be := &fakeWAFBackend{}
	if err := env.reconciler(be).Reconcile(context.Background(), "waf-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _ := env.policies.Get("waf-1")
	if got.Status.Phase != resource.PhasePending {
		t.Fatalf("phase = %q, want Pending", got.Status.Phase)
	}
	if be.chain != "" {
		t.Fatal("backend should not be called while pending")
	}
}

func TestWAFTargetNotFound(t *testing.T) {
	t.Parallel()

	env := newWAFEnv(t)
	env.putPolicy(t, "waf-1", "compute", "missing", false)
	if err := env.reconciler(&fakeWAFBackend{}).Reconcile(context.Background(), "waf-1"); err == nil {
		t.Fatal("expected an error when the target is missing")
	}
	got, _ := env.policies.Get("waf-1")
	if got.Status.Phase != resource.PhaseError {
		t.Fatalf("phase = %q, want Error", got.Status.Phase)
	}
}

func TestWAFFinalize(t *testing.T) {
	t.Parallel()

	env := newWAFEnv(t)
	env.putNetwork(t)
	c := &resource.Compute{Metadata: resource.ObjectMeta{UID: "i-1", Generation: 1}, Spec: resource.ComputeSpec{SubnetID: "sn-1", Image: "x"}}
	c.Status.IP = "10.0.1.10"
	if err := env.computes.Put(c); err != nil {
		t.Fatalf("seed compute: %v", err)
	}
	env.putPolicy(t, "waf-1", "compute", "i-1", false)
	be := &fakeWAFBackend{}
	rec := env.reconciler(be)
	if err := rec.Reconcile(context.Background(), "waf-1"); err != nil {
		t.Fatalf("reconcile up: %v", err)
	}

	cur, _ := env.policies.Get("waf-1")
	now := time.Now()
	cur.Metadata.DeletionTimestamp = &now
	if err := env.policies.Put(cur); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}
	if err := rec.Reconcile(context.Background(), "waf-1"); err != nil {
		t.Fatalf("reconcile delete: %v", err)
	}
	if _, err := env.policies.Get("waf-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("record should be removed, got %v", err)
	}
	if !be.deleted {
		t.Fatal("chain should have been deleted")
	}
}

func TestExecWAFEnsureChain(t *testing.T) {
	t.Parallel()

	rec := &fwRecorder{}
	be := manager.NewExecWAFWithRunner(rec.run)
	rules := []resource.WAFRuleSpec{
		{PolicyID: "waf-1", MatchType: "source_ip", MatchValue: "1.2.3.0/24", Action: "block"},
		{PolicyID: "waf-1", MatchType: "port", MatchValue: "22", Action: "ratelimit", RateLimit: 60, RateWindow: 60},
	}
	if err := be.EnsureChain(context.Background(), "INFRA-WAF-X", []string{"-d", "10.0.1.10"}, rules, true); err != nil {
		t.Fatalf("ensure chain: %v", err)
	}
	for _, want := range []string{"-N", "DROP", "hashlimit", "FORWARD", "LOG"} {
		if !anyCallHas(rec.calls, want) {
			t.Fatalf("missing %q in calls: %v", want, rec.calls)
		}
	}
}
