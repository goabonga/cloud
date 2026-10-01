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

type microvmSchedEnv struct {
	microvms *registry.Registry[resource.MicroVMSpec, resource.MicroVMStatus]
	nodes    *registry.Registry[resource.NodeSpec, resource.NodeStatus]
	pools    *registry.Registry[resource.NodePoolSpec, resource.NodePoolStatus]
	ctrl     *controllers.MicroVMSchedulerController
}

func newMicroVMSchedEnv(t *testing.T) *microvmSchedEnv {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	env := &microvmSchedEnv{
		microvms: registry.New[resource.MicroVMSpec, resource.MicroVMStatus](store, resource.KindMicroVM),
		nodes:    registry.New[resource.NodeSpec, resource.NodeStatus](store, resource.KindNode),
		pools:    registry.New[resource.NodePoolSpec, resource.NodePoolStatus](store, resource.KindNodePool),
	}
	env.ctrl = controllers.NewMicroVMSchedulerController(env.microvms, env.nodes, env.pools, time.Minute, nil)
	return env
}

// putNode seeds a node with a fresh heartbeat so it is schedulable.
func (env *microvmSchedEnv) putNode(t *testing.T, uid string, cpus, mem, maxPods int, labels map[string]string) {
	t.Helper()
	env.putNodeSeen(t, uid, cpus, mem, maxPods, labels, time.Now())
}

// putNodeSeen seeds a node whose last heartbeat was at lastSeen.
func (env *microvmSchedEnv) putNodeSeen(t *testing.T, uid string, cpus, mem, maxPods int, labels map[string]string, lastSeen time.Time) {
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

func (env *microvmSchedEnv) putMicroVM(t *testing.T, uid string, vcpus, mem int, poolID, nodeName string) {
	t.Helper()
	v := &resource.MicroVM{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec: resource.MicroVMSpec{
			SubnetID: "sn-1", KernelPath: "/boot/vmlinux", Image: "/images/base.raw",
			VCPUs: vcpus, MemoryMB: mem, NodePoolID: poolID,
		},
	}
	v.Status.NodeName = nodeName
	if err := env.microvms.Put(v); err != nil {
		t.Fatalf("seed microvm: %v", err)
	}
}

func TestMicroVMScheduleAssignsAndRecordsAllocation(t *testing.T) {
	t.Parallel()

	env := newMicroVMSchedEnv(t)
	env.putNode(t, "node-1", 4, 8192, 10, nil)
	env.putMicroVM(t, "vm-1", 1, 512, "", "")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	v, _ := env.microvms.Get("vm-1")
	if v.Status.NodeName != "node-1" {
		t.Fatalf("microvm not scheduled: %q", v.Status.NodeName)
	}
	n, _ := env.nodes.Get("node-1")
	if n.Status.Allocated.Pods != 1 || n.Status.Allocated.CPUs != 1 || n.Status.Allocated.MemoryMB != 512 {
		t.Fatalf("node allocation wrong: %+v", n.Status.Allocated)
	}
	if !n.Status.IsReady() {
		t.Fatalf("node phase = %q, want Ready", n.Status.Phase)
	}
	if env.ctrl.Name() != "microvm-scheduler" {
		t.Fatalf("name = %q", env.ctrl.Name())
	}
}

func TestMicroVMScheduleSpreadsByCount(t *testing.T) {
	t.Parallel()

	env := newMicroVMSchedEnv(t)
	env.putNode(t, "node-1", 8, 8192, 10, nil)
	env.putNode(t, "node-2", 8, 8192, 10, nil)
	env.putMicroVM(t, "vm-existing", 1, 256, "", "node-1") // node-1 already has one
	env.putMicroVM(t, "vm-new", 1, 256, "", "")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	v, _ := env.microvms.Get("vm-new")
	if v.Status.NodeName != "node-2" {
		t.Fatalf("expected spread to node-2, got %q", v.Status.NodeName)
	}
}

func TestMicroVMScheduleRespectsCapacity(t *testing.T) {
	t.Parallel()

	env := newMicroVMSchedEnv(t)
	env.putNode(t, "node-1", 1, 1024, 10, nil)
	env.putMicroVM(t, "vm-big", 2, 512, "", "") // needs 2 vCPUs, node has 1

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	v, _ := env.microvms.Get("vm-big")
	if v.Status.NodeName != "" {
		t.Fatalf("oversized microvm should stay unscheduled, got %q", v.Status.NodeName)
	}
}

func TestMicroVMSchedulePoolSelector(t *testing.T) {
	t.Parallel()

	env := newMicroVMSchedEnv(t)
	env.putNode(t, "node-a", 4, 8192, 10, map[string]string{"zone": "a"})
	env.putNode(t, "node-b", 4, 8192, 10, map[string]string{"zone": "b"})
	if err := env.pools.Put(&resource.NodePool{
		Metadata: resource.ObjectMeta{UID: "pool-b", Generation: 1},
		Spec:     resource.NodePoolSpec{Name: "b", NodeSelector: map[string]string{"zone": "b"}},
	}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	env.putMicroVM(t, "vm-1", 1, 256, "pool-b", "")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	v, _ := env.microvms.Get("vm-1")
	if v.Status.NodeName != "node-b" {
		t.Fatalf("microvm should land on node-b, got %q", v.Status.NodeName)
	}
	p, _ := env.pools.Get("pool-b")
	if p.Status.TotalNodes != 1 || p.Status.ReadyNodes != 1 {
		t.Fatalf("pool status wrong: total=%d ready=%d", p.Status.TotalNodes, p.Status.ReadyNodes)
	}
}

func TestMicroVMScheduleSkipsStaleNodes(t *testing.T) {
	t.Parallel()

	env := newMicroVMSchedEnv(t)
	env.putNodeSeen(t, "node-stale", 4, 8192, 10, nil, time.Now().Add(-time.Hour))
	env.putMicroVM(t, "vm-1", 1, 256, "", "")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	v, _ := env.microvms.Get("vm-1")
	if v.Status.NodeName != "" {
		t.Fatalf("microvm should not land on a stale node, got %q", v.Status.NodeName)
	}
	n, _ := env.nodes.Get("node-stale")
	if n.Status.Phase != resource.PhasePending {
		t.Fatalf("stale node phase = %q, want Pending", n.Status.Phase)
	}
}

func TestMicroVMScheduleEvictsAndReschedules(t *testing.T) {
	t.Parallel()

	env := newMicroVMSchedEnv(t)
	env.putNodeSeen(t, "node-a", 4, 8192, 10, nil, time.Now().Add(-time.Hour)) // stale
	env.putNode(t, "node-b", 4, 8192, 10, nil)                                 // fresh
	env.putMicroVM(t, "vm-1", 1, 256, "", "node-a")                            // placed on the stale node

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	v, _ := env.microvms.Get("vm-1")
	if v.Status.NodeName != "node-b" {
		t.Fatalf("microvm should be rescheduled to node-b, got %q", v.Status.NodeName)
	}
	a, _ := env.nodes.Get("node-a")
	if a.Status.Allocated.Pods != 0 {
		t.Fatalf("stale node should hold no allocation: %+v", a.Status.Allocated)
	}
	b, _ := env.nodes.Get("node-b")
	if b.Status.Allocated.Pods != 1 {
		t.Fatalf("live node should hold the rescheduled microvm: %+v", b.Status.Allocated)
	}
}

func TestMicroVMScheduleEvictsFromDeletedNode(t *testing.T) {
	t.Parallel()

	env := newMicroVMSchedEnv(t)
	env.putMicroVM(t, "vm-1", 1, 256, "", "node-gone") // node no longer exists

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	v, _ := env.microvms.Get("vm-1")
	if v.Status.NodeName != "" {
		t.Fatalf("placement on a deleted node should be cleared, got %q", v.Status.NodeName)
	}
}

func TestMicroVMScheduleMissingPoolLeavesUnscheduled(t *testing.T) {
	t.Parallel()

	env := newMicroVMSchedEnv(t)
	env.putNode(t, "node-1", 4, 8192, 10, nil)
	env.putMicroVM(t, "vm-1", 1, 256, "pool-missing", "")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	v, _ := env.microvms.Get("vm-1")
	if v.Status.NodeName != "" {
		t.Fatalf("microvm referencing a missing pool should stay unscheduled, got %q", v.Status.NodeName)
	}
}
