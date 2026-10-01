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

type asyncReplicaSchedEnv struct {
	replicas *registry.Registry[resource.AsyncDiskReplicaSpec, resource.AsyncDiskReplicaStatus]
	disks    *registry.Registry[resource.DiskSpec, resource.DiskStatus]
	nodes    *registry.Registry[resource.NodeSpec, resource.NodeStatus]
	pools    *registry.Registry[resource.NodePoolSpec, resource.NodePoolStatus]
	ctrl     *controllers.AsyncDiskReplicaSchedulerController
}

func newAsyncReplicaSchedEnv(t *testing.T) *asyncReplicaSchedEnv {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	env := &asyncReplicaSchedEnv{
		replicas: registry.New[resource.AsyncDiskReplicaSpec, resource.AsyncDiskReplicaStatus](store, resource.KindAsyncDiskReplica),
		disks:    registry.New[resource.DiskSpec, resource.DiskStatus](store, resource.KindDisk),
		nodes:    registry.New[resource.NodeSpec, resource.NodeStatus](store, resource.KindNode),
		pools:    registry.New[resource.NodePoolSpec, resource.NodePoolStatus](store, resource.KindNodePool),
	}
	env.ctrl = controllers.NewAsyncDiskReplicaSchedulerController(env.replicas, env.disks, env.nodes, env.pools, time.Minute, nil)
	return env
}

func (env *asyncReplicaSchedEnv) putNode(t *testing.T, uid string, labels map[string]string) {
	t.Helper()
	env.putNodeSeen(t, uid, labels, time.Now())
}

func (env *asyncReplicaSchedEnv) putNodeSeen(t *testing.T, uid string, labels map[string]string, lastSeen time.Time) {
	t.Helper()
	n := &resource.Node{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.NodeSpec{Hostname: uid, Address: "10.0.0.2", Labels: labels, Capacity: resource.NodeCapacity{CPUs: 4, MemoryMB: 8192}},
	}
	n.Status.LastSeen = lastSeen.UTC().Format(time.RFC3339)
	if err := env.nodes.Put(n); err != nil {
		t.Fatalf("seed node: %v", err)
	}
}

func (env *asyncReplicaSchedEnv) putDisk(t *testing.T, uid, nodeName string) {
	t.Helper()
	d := &resource.Disk{Metadata: resource.ObjectMeta{UID: uid, Generation: 1}, Spec: resource.DiskSpec{SizeMB: 1024}}
	d.Status.NodeName = nodeName
	if err := env.disks.Put(d); err != nil {
		t.Fatalf("seed disk: %v", err)
	}
}

func (env *asyncReplicaSchedEnv) putReplica(t *testing.T, uid, diskID, poolID, nodeName string) {
	t.Helper()
	r := &resource.AsyncDiskReplica{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.AsyncDiskReplicaSpec{DiskID: diskID, TargetNodePoolID: poolID},
	}
	r.Status.NodeName = nodeName
	if err := env.replicas.Put(r); err != nil {
		t.Fatalf("seed replica: %v", err)
	}
}

func TestAsyncDiskReplicaScheduleNeverLandsOnTheDisksOwnNode(t *testing.T) {
	t.Parallel()

	env := newAsyncReplicaSchedEnv(t)
	env.putNode(t, "node-a", nil)
	env.putNode(t, "node-b", nil)
	env.putDisk(t, "disk-1", "node-a")
	env.putReplica(t, "replica-1", "disk-1", "", "")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	r, _ := env.replicas.Get("replica-1")
	if r.Status.NodeName != "node-b" {
		t.Fatalf("replica should land on node-b (not the disk's node-a), got %q", r.Status.NodeName)
	}
	if env.ctrl.Name() != "async-disk-replica-scheduler" {
		t.Fatalf("name = %q", env.ctrl.Name())
	}
}

func TestAsyncDiskReplicaScheduleStaysUnscheduledWithNoOtherNode(t *testing.T) {
	t.Parallel()

	env := newAsyncReplicaSchedEnv(t)
	env.putNode(t, "node-a", nil) // the only ready node is the disk's own
	env.putDisk(t, "disk-1", "node-a")
	env.putReplica(t, "replica-1", "disk-1", "", "")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	r, _ := env.replicas.Get("replica-1")
	if r.Status.NodeName != "" {
		t.Fatalf("replica should stay unscheduled rather than land on the disk's own node, got %q", r.Status.NodeName)
	}
}

func TestAsyncDiskReplicaScheduleWaitsForTheDiskToBePlaced(t *testing.T) {
	t.Parallel()

	env := newAsyncReplicaSchedEnv(t)
	env.putNode(t, "node-a", nil)
	env.putDisk(t, "disk-1", "") // not placed yet
	env.putReplica(t, "replica-1", "disk-1", "", "")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	r, _ := env.replicas.Get("replica-1")
	if r.Status.NodeName != "" {
		t.Fatalf("replica should wait for the disk to be placed, got %q", r.Status.NodeName)
	}
}

func TestAsyncDiskReplicaScheduleRespectsTargetPool(t *testing.T) {
	t.Parallel()

	env := newAsyncReplicaSchedEnv(t)
	env.putNode(t, "node-a", map[string]string{"zone": "a"})
	env.putNode(t, "node-b", map[string]string{"zone": "b"})
	env.putNode(t, "node-c", map[string]string{"zone": "b"})
	if err := env.pools.Put(&resource.NodePool{
		Metadata: resource.ObjectMeta{UID: "pool-b", Generation: 1},
		Spec:     resource.NodePoolSpec{Name: "b", NodeSelector: map[string]string{"zone": "b"}},
	}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	env.putDisk(t, "disk-1", "node-a")
	env.putReplica(t, "replica-1", "disk-1", "pool-b", "")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	r, _ := env.replicas.Get("replica-1")
	if r.Status.NodeName != "node-b" && r.Status.NodeName != "node-c" {
		t.Fatalf("replica should land in pool-b, got %q", r.Status.NodeName)
	}
}

func TestAsyncDiskReplicaScheduleMissingPoolLeavesUnscheduled(t *testing.T) {
	t.Parallel()

	env := newAsyncReplicaSchedEnv(t)
	env.putNode(t, "node-a", nil)
	env.putNode(t, "node-b", nil)
	env.putDisk(t, "disk-1", "node-a")
	env.putReplica(t, "replica-1", "disk-1", "pool-missing", "")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	r, _ := env.replicas.Get("replica-1")
	if r.Status.NodeName != "" {
		t.Fatalf("replica referencing a missing pool should stay unscheduled, got %q", r.Status.NodeName)
	}
}

func TestAsyncDiskReplicaScheduleEvictsAndReschedules(t *testing.T) {
	t.Parallel()

	env := newAsyncReplicaSchedEnv(t)
	env.putDisk(t, "disk-1", "node-primary")
	env.putNodeSeen(t, "node-stale", nil, time.Now().Add(-time.Hour))
	env.putNode(t, "node-fresh", nil)
	env.putReplica(t, "replica-1", "disk-1", "", "node-stale")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	r, _ := env.replicas.Get("replica-1")
	if r.Status.NodeName != "node-fresh" {
		t.Fatalf("replica should be rescheduled to node-fresh, got %q", r.Status.NodeName)
	}
}

func TestAsyncDiskReplicaScheduleEvictsFromDeletedNode(t *testing.T) {
	t.Parallel()

	env := newAsyncReplicaSchedEnv(t)
	env.putDisk(t, "disk-1", "node-primary")
	env.putReplica(t, "replica-1", "disk-1", "", "node-gone")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	r, _ := env.replicas.Get("replica-1")
	if r.Status.NodeName != "" {
		t.Fatalf("placement on a deleted node should be cleared, got %q", r.Status.NodeName)
	}
}
