// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package controllers_test

import (
	"context"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/controllers"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

type schedEnv struct {
	computes *registry.Registry[resource.ComputeSpec, resource.ComputeStatus]
	nodes    *registry.Registry[resource.NodeSpec, resource.NodeStatus]
	pools    *registry.Registry[resource.NodePoolSpec, resource.NodePoolStatus]
	ctrl     *controllers.SchedulerController
}

func newSchedEnv(t *testing.T) *schedEnv {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	env := &schedEnv{
		computes: registry.New[resource.ComputeSpec, resource.ComputeStatus](store, resource.KindCompute),
		nodes:    registry.New[resource.NodeSpec, resource.NodeStatus](store, resource.KindNode),
		pools:    registry.New[resource.NodePoolSpec, resource.NodePoolStatus](store, resource.KindNodePool),
	}
	env.ctrl = controllers.NewSchedulerController(env.computes, env.nodes, env.pools, time.Minute, nil)
	return env
}

// putNode seeds a node with a fresh heartbeat so it is schedulable.
func (env *schedEnv) putNode(t *testing.T, uid string, cpus, mem, maxPods int, labels map[string]string) {
	t.Helper()
	env.putNodeSeen(t, uid, cpus, mem, maxPods, labels, time.Now())
}

// putNodeSeen seeds a node whose last heartbeat was at lastSeen.
func (env *schedEnv) putNodeSeen(t *testing.T, uid string, cpus, mem, maxPods int, labels map[string]string, lastSeen time.Time) {
	t.Helper()
	n := &resource.Node{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.NodeSpec{Hostname: uid, Address: "10.0.0.2", Labels: labels, Capacity: resource.NodeCapacity{CPUs: cpus, MemoryMB: mem, MaxPods: maxPods}},
	}
	n.Status.LastSeen = lastSeen.UTC().Format(time.RFC3339)
	if err := env.nodes.Put(n); err != nil {
		t.Fatalf("seed node: %v", err)
	}
}

func (env *schedEnv) putCompute(t *testing.T, uid string, cpu float64, mem int, poolID, nodeName string) {
	t.Helper()
	c := &resource.Compute{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.ComputeSpec{SubnetID: "sn-1", Image: "x", CPU: cpu, MemoryMB: mem, NodePoolID: poolID},
	}
	c.Status.NodeName = nodeName
	if err := env.computes.Put(c); err != nil {
		t.Fatalf("seed compute: %v", err)
	}
}

func TestScheduleAssignsAndRecordsAllocation(t *testing.T) {
	t.Parallel()

	env := newSchedEnv(t)
	env.putNode(t, "node-1", 4, 8192, 10, nil)
	env.putCompute(t, "i-1", 1, 512, "", "")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	c, _ := env.computes.Get("i-1")
	if c.Status.NodeName != "node-1" {
		t.Fatalf("compute not scheduled: %q", c.Status.NodeName)
	}
	n, _ := env.nodes.Get("node-1")
	if n.Status.Allocated.Pods != 1 || n.Status.Allocated.CPUs != 1 || n.Status.Allocated.MemoryMB != 512 {
		t.Fatalf("node allocation wrong: %+v", n.Status.Allocated)
	}
	if !n.Status.IsReady() {
		t.Fatalf("node phase = %q, want Ready", n.Status.Phase)
	}
	if env.ctrl.Name() != "scheduler" {
		t.Fatalf("name = %q", env.ctrl.Name())
	}
}

func TestScheduleSpreadsByPodCount(t *testing.T) {
	t.Parallel()

	env := newSchedEnv(t)
	env.putNode(t, "node-1", 8, 8192, 10, nil)
	env.putNode(t, "node-2", 8, 8192, 10, nil)
	env.putCompute(t, "i-existing", 1, 256, "", "node-1") // node-1 already has a pod
	env.putCompute(t, "i-new", 1, 256, "", "")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	c, _ := env.computes.Get("i-new")
	if c.Status.NodeName != "node-2" {
		t.Fatalf("expected spread to node-2, got %q", c.Status.NodeName)
	}
}

func TestScheduleRespectsCapacity(t *testing.T) {
	t.Parallel()

	env := newSchedEnv(t)
	env.putNode(t, "node-1", 1, 1024, 10, nil)
	env.putCompute(t, "i-big", 2, 512, "", "") // needs 2 CPUs, node has 1

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	c, _ := env.computes.Get("i-big")
	if c.Status.NodeName != "" {
		t.Fatalf("oversized compute should stay unscheduled, got %q", c.Status.NodeName)
	}
}

func TestSchedulePoolSelector(t *testing.T) {
	t.Parallel()

	env := newSchedEnv(t)
	env.putNode(t, "node-a", 4, 8192, 10, map[string]string{"zone": "a"})
	env.putNode(t, "node-b", 4, 8192, 10, map[string]string{"zone": "b"})
	if err := env.pools.Put(&resource.NodePool{
		Metadata: resource.ObjectMeta{UID: "pool-b", Generation: 1},
		Spec:     resource.NodePoolSpec{Name: "b", NodeSelector: map[string]string{"zone": "b"}},
	}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	env.putCompute(t, "i-1", 1, 256, "pool-b", "")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	c, _ := env.computes.Get("i-1")
	if c.Status.NodeName != "node-b" {
		t.Fatalf("compute should land on node-b, got %q", c.Status.NodeName)
	}
	p, _ := env.pools.Get("pool-b")
	if p.Status.TotalNodes != 1 || p.Status.ReadyNodes != 1 {
		t.Fatalf("pool status wrong: total=%d ready=%d", p.Status.TotalNodes, p.Status.ReadyNodes)
	}
}

func TestScheduleSkipsStaleNodes(t *testing.T) {
	t.Parallel()

	env := newSchedEnv(t)
	env.putNodeSeen(t, "node-stale", 4, 8192, 10, nil, time.Now().Add(-time.Hour))
	env.putCompute(t, "i-1", 1, 256, "", "")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	c, _ := env.computes.Get("i-1")
	if c.Status.NodeName != "" {
		t.Fatalf("compute should not land on a stale node, got %q", c.Status.NodeName)
	}
	n, _ := env.nodes.Get("node-stale")
	if n.Status.Phase != resource.PhasePending {
		t.Fatalf("stale node phase = %q, want Pending", n.Status.Phase)
	}
}

func TestScheduleNeverSeenNodeNotReady(t *testing.T) {
	t.Parallel()

	env := newSchedEnv(t)
	if err := env.nodes.Put(&resource.Node{
		Metadata: resource.ObjectMeta{UID: "node-1", Generation: 1},
		Spec:     resource.NodeSpec{Hostname: "h1", Address: "10.0.0.2", Capacity: resource.NodeCapacity{CPUs: 4, MemoryMB: 8192, MaxPods: 10}},
	}); err != nil { // no LastSeen
		t.Fatalf("seed node: %v", err)
	}
	env.putCompute(t, "i-1", 1, 256, "", "")
	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if c, _ := env.computes.Get("i-1"); c.Status.NodeName != "" {
		t.Fatalf("compute should not land on a never-seen node, got %q", c.Status.NodeName)
	}
}

func TestScheduleEvictsAndReschedules(t *testing.T) {
	t.Parallel()

	env := newSchedEnv(t)
	env.putNodeSeen(t, "node-a", 4, 8192, 10, nil, time.Now().Add(-time.Hour)) // stale
	env.putNode(t, "node-b", 4, 8192, 10, nil)                                 // fresh
	env.putCompute(t, "i-1", 1, 256, "", "node-a")                             // placed on the stale node

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	c, _ := env.computes.Get("i-1")
	if c.Status.NodeName != "node-b" {
		t.Fatalf("compute should be rescheduled to node-b, got %q", c.Status.NodeName)
	}
	a, _ := env.nodes.Get("node-a")
	if a.Status.Allocated.Pods != 0 {
		t.Fatalf("stale node should hold no allocation: %+v", a.Status.Allocated)
	}
	b, _ := env.nodes.Get("node-b")
	if b.Status.Allocated.Pods != 1 {
		t.Fatalf("live node should hold the rescheduled pod: %+v", b.Status.Allocated)
	}
}

func TestScheduleEvictsFromDeletedNode(t *testing.T) {
	t.Parallel()

	env := newSchedEnv(t)
	env.putCompute(t, "i-1", 1, 256, "", "node-gone") // node no longer exists

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	c, _ := env.computes.Get("i-1")
	if c.Status.NodeName != "" {
		t.Fatalf("placement on a deleted node should be cleared, got %q", c.Status.NodeName)
	}
}

func TestScheduleMissingPoolLeavesUnscheduled(t *testing.T) {
	t.Parallel()

	env := newSchedEnv(t)
	env.putNode(t, "node-1", 4, 8192, 10, nil)
	env.putCompute(t, "i-1", 1, 256, "pool-missing", "")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	c, _ := env.computes.Get("i-1")
	if c.Status.NodeName != "" {
		t.Fatalf("compute referencing a missing pool should stay unscheduled, got %q", c.Status.NodeName)
	}
}

func TestSchedulerAccountsForLegacyZeroLimits(t *testing.T) {
	env := newSchedEnv(t)
	env.putNode(t, "node-1", 1, 256, 10, nil)
	env.putCompute(t, "a", 0, 0, "", "")
	env.putCompute(t, "b", 0, 0, "", "")
	if err := env.ctrl.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	assigned := 0
	for _, uid := range []string{"a", "b"} {
		cp, err := env.computes.Get(uid)
		if err != nil {
			t.Fatal(err)
		}
		if cp.Status.NodeName != "" {
			assigned++
		}
	}
	if assigned != 1 {
		t.Fatalf("oversubscribed default limits: %d assigned", assigned)
	}
	node, err := env.nodes.Get("node-1")
	if err != nil || node.Status.Allocated.CPUs != 1 || node.Status.Allocated.MemoryMB != 256 {
		t.Fatalf("allocation=%+v, %v", node, err)
	}
}
