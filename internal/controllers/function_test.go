// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package controllers_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/controllers"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

type fnEnv struct {
	functions *registry.Registry[resource.FunctionSpec, resource.FunctionStatus]
	instances *registry.Registry[resource.FunctionInstanceSpec, resource.FunctionInstanceStatus]
	computes  *registry.Registry[resource.ComputeSpec, resource.ComputeStatus]
	subnets   *registry.Registry[resource.SubnetSpec, resource.SubnetStatus]
	vpcs      *registry.Registry[resource.VPCSpec, resource.VPCStatus]
	sgs       *registry.Registry[resource.SecurityGroupSpec, resource.SecurityGroupStatus]

	now  time.Time
	ctrl *controllers.FunctionController
}

func newFnEnv(t *testing.T) *fnEnv {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	env := &fnEnv{
		functions: registry.New[resource.FunctionSpec, resource.FunctionStatus](store, resource.KindFunction),
		instances: registry.New[resource.FunctionInstanceSpec, resource.FunctionInstanceStatus](store, resource.KindFunctionInstance),
		computes:  registry.New[resource.ComputeSpec, resource.ComputeStatus](store, resource.KindCompute),
		subnets:   registry.New[resource.SubnetSpec, resource.SubnetStatus](store, resource.KindSubnet),
		vpcs:      registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC),
		sgs:       registry.New[resource.SecurityGroupSpec, resource.SecurityGroupStatus](store, resource.KindSecurityGroup),
		now:       time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	env.ctrl = controllers.NewFunctionController(env.functions, env.instances, env.computes, env.subnets, env.vpcs, env.sgs,
		func() time.Time { return env.now }, nil)
	return env
}

// seedNetwork puts a VPC and subnet whose dependency checks already resolve,
// so a function referencing sn-1 reaches Ready immediately.
func (env *fnEnv) seedNetwork(t *testing.T) {
	t.Helper()
	vpc := &resource.VPC{Metadata: resource.ObjectMeta{UID: "vpc-1"}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	vpc.Status.BridgeName = "br-1"
	if err := env.vpcs.Put(vpc); err != nil {
		t.Fatalf("seed vpc: %v", err)
	}
	subnet := &resource.Subnet{Metadata: resource.ObjectMeta{UID: "sn-1"}, Spec: resource.SubnetSpec{VPCID: "vpc-1", CIDR: "10.0.0.0/24"}}
	subnet.Status.Gateway = "10.0.0.1"
	if err := env.subnets.Put(subnet); err != nil {
		t.Fatalf("seed subnet: %v", err)
	}
}

func (env *fnEnv) putFunction(t *testing.T, uid string, policy resource.WarmPoolPolicy) *resource.Function {
	t.Helper()
	fn := &resource.Function{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.FunctionSpec{SubnetID: "sn-1", Image: "example/fn:latest", Port: 8080, WarmPool: policy},
	}
	if err := env.functions.Put(fn); err != nil {
		t.Fatalf("seed function: %v", err)
	}
	return fn
}

// markComputesReady flips every compute labelled for fn to Ready, standing in
// for the agent's own reconcile pass realizing them.
func (env *fnEnv) markComputesReady(t *testing.T) {
	t.Helper()
	computes, err := env.computes.List()
	if err != nil {
		t.Fatalf("list computes: %v", err)
	}
	for i := range computes {
		cp := &computes[i]
		if cp.Status.Phase == resource.PhaseReady {
			continue
		}
		cp.Status.SetPhase(resource.PhaseReady, "Running", "instance ready")
		if err := env.computes.Put(cp); err != nil {
			t.Fatalf("mark compute ready: %v", err)
		}
	}
}

func (env *fnEnv) reconcile(t *testing.T) {
	t.Helper()
	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

func (env *fnEnv) instancesFor(t *testing.T, functionID string) []resource.FunctionInstance {
	t.Helper()
	all, err := env.instances.List()
	if err != nil {
		t.Fatalf("list instances: %v", err)
	}
	var out []resource.FunctionInstance
	for _, inst := range all {
		if inst.Spec.FunctionID == functionID {
			out = append(out, inst)
		}
	}
	return out
}

func TestFunctionController_FillsToMinWarm(t *testing.T) {
	t.Parallel()
	env := newFnEnv(t)
	env.seedNetwork(t)
	env.putFunction(t, "fn-1", resource.WarmPoolPolicy{MinWarm: 2})

	env.reconcile(t)

	insts := env.instancesFor(t, "fn-1")
	if len(insts) != 2 {
		t.Fatalf("want 2 function instances after fill, got %d", len(insts))
	}
	fn, err := env.functions.Get("fn-1")
	if err != nil {
		t.Fatalf("get function: %v", err)
	}
	if fn.Status.Phase != resource.PhaseReady {
		t.Fatalf("want function Ready once dependencies resolve, got %s", fn.Status.Phase)
	}
	if fn.Status.WarmCount != 0 {
		t.Fatalf("want WarmCount 0 before any backing compute is ready, got %d", fn.Status.WarmCount)
	}

	// Once the agent realizes the backing computes, the next pass counts
	// them as live and stops creating more.
	env.markComputesReady(t)
	env.reconcile(t)

	insts = env.instancesFor(t, "fn-1")
	if len(insts) != 2 {
		t.Fatalf("want pool to stay at 2 once warm, got %d", len(insts))
	}
	fn, err = env.functions.Get("fn-1")
	if err != nil {
		t.Fatalf("get function: %v", err)
	}
	if fn.Status.WarmCount != 2 {
		t.Fatalf("want WarmCount 2 once computes are ready, got %d", fn.Status.WarmCount)
	}
	for _, inst := range insts {
		if inst.Status.Phase != resource.PhaseReady || inst.Status.State != resource.FunctionInstanceWarm {
			t.Fatalf("want instance Ready+Warm, got phase=%s state=%s", inst.Status.Phase, inst.Status.State)
		}
	}
}

func TestFunctionController_PendingWithoutDependencies(t *testing.T) {
	t.Parallel()
	env := newFnEnv(t)
	// No VPC/subnet seeded: dependencies never resolve.
	env.putFunction(t, "fn-1", resource.WarmPoolPolicy{MinWarm: 1})

	env.reconcile(t)

	fn, err := env.functions.Get("fn-1")
	if err != nil {
		t.Fatalf("get function: %v", err)
	}
	if fn.Status.Phase != resource.PhasePending {
		t.Fatalf("want Pending without a subnet, got %s", fn.Status.Phase)
	}
	if insts := env.instancesFor(t, "fn-1"); len(insts) != 0 {
		t.Fatalf("want no pool instances before dependencies resolve, got %d", len(insts))
	}
}

func TestFunctionController_EvictsIdleAboveMinWarmFloor(t *testing.T) {
	t.Parallel()
	env := newFnEnv(t)
	env.seedNetwork(t)
	policy := resource.WarmPoolPolicy{MinWarm: 1, IdleTTLSeconds: 5}
	env.putFunction(t, "fn-1", policy)

	// Seed three already-warm, already-ready instances directly, bypassing
	// fill, with distinct LastUsedAt times: oldest, middle, newest.
	times := []time.Time{
		env.now.Add(-20 * time.Second), // oldest: beyond TTL, beyond the floor -> evicted
		env.now.Add(-10 * time.Second), // middle: beyond TTL, beyond the floor -> evicted
		env.now.Add(-1 * time.Second),  // newest: within the MinWarm=1 floor -> kept
	}
	for _, lu := range times {
		env.seedWarmInstance(t, "fn-1", lu)
	}

	env.reconcile(t)

	// Eviction only soft-marks an instance (DeletionTimestamp set); it is
	// removed once a later pass confirms its compute has torn down. So the
	// pool's live contents are the non-deleting instances, not the raw count.
	live := nonDeleting(env.instancesFor(t, "fn-1"))
	if len(live) != 1 {
		t.Fatalf("want 1 live instance left (the MinWarm floor), got %d", len(live))
	}
	if live[0].Status.LastUsedAt != times[2].UTC().Format(time.RFC3339) {
		t.Fatalf("want the most recently used instance kept, got LastUsedAt=%s", live[0].Status.LastUsedAt)
	}

	fn, err := env.functions.Get("fn-1")
	if err != nil {
		t.Fatalf("get function: %v", err)
	}
	if fn.Status.WarmCount != 1 {
		t.Fatalf("want WarmCount 1 after eviction, got %d", fn.Status.WarmCount)
	}
}

func TestFunctionController_EnforcesMaxWarmCap(t *testing.T) {
	t.Parallel()
	env := newFnEnv(t)
	env.seedNetwork(t)
	policy := resource.WarmPoolPolicy{MinWarm: 2, MaxWarm: 2, IdleTTLSeconds: 1000}
	env.putFunction(t, "fn-1", policy)

	// Three warm, ready, recently-used instances: MaxWarm=2 must evict one
	// even though none of them are idle past the TTL and MinWarm=2 would
	// otherwise keep both.
	for i := 0; i < 3; i++ {
		env.seedWarmInstance(t, "fn-1", env.now.Add(-time.Duration(i)*time.Second))
	}

	env.reconcile(t)

	live := nonDeleting(env.instancesFor(t, "fn-1"))
	if len(live) != 2 {
		t.Fatalf("want MaxWarm=2 enforced, got %d live instances", len(live))
	}
}

func TestFunctionController_AllocatesDistinctPortsAndMirrorsNodeName(t *testing.T) {
	t.Parallel()
	env := newFnEnv(t)
	env.seedNetwork(t)
	env.putFunction(t, "fn-1", resource.WarmPoolPolicy{MinWarm: 2})

	env.reconcile(t)

	insts := env.instancesFor(t, "fn-1")
	if len(insts) != 2 {
		t.Fatalf("want 2 instances, got %d", len(insts))
	}
	if insts[0].Status.Port == 0 || insts[1].Status.Port == 0 {
		t.Fatalf("want both instances to have an allocated port, got %+v and %+v", insts[0].Status, insts[1].Status)
	}
	if insts[0].Status.Port == insts[1].Status.Port {
		t.Fatalf("want distinct ports, both got %d", insts[0].Status.Port)
	}
	for _, inst := range insts {
		cp, err := env.computes.Get(inst.Spec.ComputeID)
		if err != nil {
			t.Fatalf("get compute: %v", err)
		}
		want := fmt.Sprintf("%d:8080/tcp", inst.Status.Port)
		if len(cp.Spec.Ports) != 1 || cp.Spec.Ports[0] != want {
			t.Fatalf("compute ports = %v, want [%s]", cp.Spec.Ports, want)
		}
	}

	// Simulate the scheduler placing the computes onto a node, then the agent
	// realizing them - both reflected by the compute's own status fields.
	for _, inst := range insts {
		cp, err := env.computes.Get(inst.Spec.ComputeID)
		if err != nil {
			t.Fatalf("get compute: %v", err)
		}
		cp.Status.NodeName = "node-1"
		cp.Status.SetPhase(resource.PhaseReady, "Running", "instance ready")
		if err := env.computes.Put(cp); err != nil {
			t.Fatalf("mark compute ready: %v", err)
		}
	}
	env.reconcile(t)

	for _, inst := range env.instancesFor(t, "fn-1") {
		updated, err := env.instances.Get(inst.Metadata.UID)
		if err != nil {
			t.Fatalf("get instance: %v", err)
		}
		if updated.Status.NodeName != "node-1" {
			t.Fatalf("want NodeName mirrored from the backing compute, got %q", updated.Status.NodeName)
		}
	}
}

func TestFunctionController_CascadeDeletesOnFunctionDeletion(t *testing.T) {
	t.Parallel()
	env := newFnEnv(t)
	env.seedNetwork(t)
	env.putFunction(t, "fn-1", resource.WarmPoolPolicy{MinWarm: 1})

	env.reconcile(t)
	env.markComputesReady(t)
	env.reconcile(t)

	insts := env.instancesFor(t, "fn-1")
	if len(insts) != 1 {
		t.Fatalf("setup: want 1 warm instance, got %d", len(insts))
	}
	computeID := insts[0].Spec.ComputeID

	fn, err := env.functions.Get("fn-1")
	if err != nil {
		t.Fatalf("get function: %v", err)
	}
	now := env.now
	fn.Metadata.DeletionTimestamp = &now
	if err := env.functions.Put(fn); err != nil {
		t.Fatalf("mark function deleting: %v", err)
	}

	env.reconcile(t)

	// The instance and its compute are marked for deletion but the compute
	// hasn't "torn down" yet (nothing simulates the agent here), so the
	// function itself must still be present, waiting on its finalizer.
	if _, err := env.functions.Get("fn-1"); err != nil {
		t.Fatalf("want function to still exist pending finalization, got: %v", err)
	}
	cp, err := env.computes.Get(computeID)
	if err != nil {
		t.Fatalf("get compute: %v", err)
	}
	if !cp.Metadata.IsDeleting() {
		t.Fatalf("want backing compute marked for deletion")
	}

	// Once the agent finishes tearing down the compute (removes it from the
	// store), the next pass finalizes the instance; the pass after that
	// observes the function now has no instances left and finalizes it too
	// (finalizeFunction works off the instance snapshot taken at the start of
	// Reconcile, so it converges one tick behind finalizeInstance within the
	// same pass - both controllers run on every tick in production).
	if err := env.computes.Delete(computeID); err != nil {
		t.Fatalf("simulate agent teardown: %v", err)
	}
	env.reconcile(t)
	env.reconcile(t)

	if _, err := env.instances.Get(insts[0].Metadata.UID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("want instance gone once its compute is torn down, got err=%v", err)
	}
	if _, err := env.functions.Get("fn-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("want function gone once its last instance is finalized, got err=%v", err)
	}
}

// nonDeleting filters out instances already marked for deletion, which stay
// in the store (soft-deleted, awaiting their finalizer) until a later pass
// confirms their backing compute has actually torn down.
func nonDeleting(insts []resource.FunctionInstance) []resource.FunctionInstance {
	out := make([]resource.FunctionInstance, 0, len(insts))
	for _, inst := range insts {
		if !inst.Metadata.IsDeleting() {
			out = append(out, inst)
		}
	}
	return out
}

// seedWarmInstance directly creates a Ready compute and a Warm, Ready
// function instance pointing at it, bypassing the controller's own fill
// logic so tests can set up pool contents precisely.
func (env *fnEnv) seedWarmInstance(t *testing.T, functionID string, lastUsedAt time.Time) {
	t.Helper()
	computeUID := "compute-" + functionID + "-" + lastUsedAt.Format("150405.000000000")
	cp := &resource.Compute{
		Metadata: resource.ObjectMeta{UID: computeUID, Generation: 1},
		Spec:     resource.ComputeSpec{SubnetID: "sn-1", Image: "example/fn:latest"},
	}
	cp.Status.SetPhase(resource.PhaseReady, "Running", "instance ready")
	if err := env.computes.Put(cp); err != nil {
		t.Fatalf("seed compute: %v", err)
	}

	inst := &resource.FunctionInstance{
		Metadata: resource.ObjectMeta{UID: computeUID + "-inst", Generation: 1},
		Spec:     resource.FunctionInstanceSpec{FunctionID: functionID, ComputeID: computeUID},
	}
	inst.Metadata.AddFinalizer(resource.FunctionInstanceFinalizer)
	inst.Status.State = resource.FunctionInstanceWarm
	inst.Status.LastUsedAt = lastUsedAt.UTC().Format(time.RFC3339)
	inst.Status.SetPhase(resource.PhaseReady, "Synced", "tracks backing compute")
	if err := env.instances.Put(inst); err != nil {
		t.Fatalf("seed function instance: %v", err)
	}
}
