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

type diskSchedEnv struct {
	disks *registry.Registry[resource.DiskSpec, resource.DiskStatus]
	nodes *registry.Registry[resource.NodeSpec, resource.NodeStatus]
	ctrl  *controllers.DiskSchedulerController
}

func newDiskSchedEnv(t *testing.T) *diskSchedEnv {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	env := &diskSchedEnv{
		disks: registry.New[resource.DiskSpec, resource.DiskStatus](store, resource.KindDisk),
		nodes: registry.New[resource.NodeSpec, resource.NodeStatus](store, resource.KindNode),
	}
	env.ctrl = controllers.NewDiskSchedulerController(env.disks, env.nodes, time.Minute, nil)
	return env
}

// putNode seeds a node with a fresh heartbeat so it is schedulable.
func (env *diskSchedEnv) putNode(t *testing.T, uid string) {
	t.Helper()
	env.putNodeSeen(t, uid, time.Now())
}

// putNodeSeen seeds a node whose last heartbeat was at lastSeen.
func (env *diskSchedEnv) putNodeSeen(t *testing.T, uid string, lastSeen time.Time) {
	t.Helper()
	n := &resource.Node{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.NodeSpec{Hostname: uid, Address: "10.0.0.2", Capacity: resource.NodeCapacity{CPUs: 4, MemoryMB: 8192}},
	}
	n.Status.LastSeen = lastSeen.UTC().Format(time.RFC3339)
	if err := env.nodes.Put(n); err != nil {
		t.Fatalf("seed node: %v", err)
	}
}

func (env *diskSchedEnv) putDisk(t *testing.T, uid, nodeName string) {
	t.Helper()
	d := &resource.Disk{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.DiskSpec{SizeMB: 1024},
	}
	d.Status.NodeName = nodeName
	if err := env.disks.Put(d); err != nil {
		t.Fatalf("seed disk: %v", err)
	}
}

func TestDiskScheduleAssigns(t *testing.T) {
	t.Parallel()

	env := newDiskSchedEnv(t)
	env.putNode(t, "node-1")
	env.putDisk(t, "disk-1", "")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	d, _ := env.disks.Get("disk-1")
	if d.Status.NodeName != "node-1" {
		t.Fatalf("disk not scheduled: %q", d.Status.NodeName)
	}
	if env.ctrl.Name() != "disk-scheduler" {
		t.Fatalf("name = %q", env.ctrl.Name())
	}
}

func TestDiskScheduleSpreadsByCount(t *testing.T) {
	t.Parallel()

	env := newDiskSchedEnv(t)
	env.putNode(t, "node-1")
	env.putNode(t, "node-2")
	env.putDisk(t, "disk-existing", "node-1") // node-1 already has one
	env.putDisk(t, "disk-new", "")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	d, _ := env.disks.Get("disk-new")
	if d.Status.NodeName != "node-2" {
		t.Fatalf("expected spread to node-2, got %q", d.Status.NodeName)
	}
}

func TestDiskScheduleSkipsStaleNodes(t *testing.T) {
	t.Parallel()

	env := newDiskSchedEnv(t)
	env.putNodeSeen(t, "node-stale", time.Now().Add(-time.Hour))
	env.putDisk(t, "disk-1", "")

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	d, _ := env.disks.Get("disk-1")
	if d.Status.NodeName != "" {
		t.Fatalf("disk should not land on a stale node, got %q", d.Status.NodeName)
	}
}

func TestDiskScheduleEvictsAndReschedules(t *testing.T) {
	t.Parallel()

	env := newDiskSchedEnv(t)
	env.putNodeSeen(t, "node-a", time.Now().Add(-time.Hour)) // stale
	env.putNode(t, "node-b")                                 // fresh
	env.putDisk(t, "disk-1", "node-a")                       // placed on the stale node

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	d, _ := env.disks.Get("disk-1")
	if d.Status.NodeName != "node-b" {
		t.Fatalf("disk should be rescheduled to node-b, got %q", d.Status.NodeName)
	}
}

func TestDiskScheduleEvictsFromDeletedNode(t *testing.T) {
	t.Parallel()

	env := newDiskSchedEnv(t)
	env.putDisk(t, "disk-1", "node-gone") // node no longer exists

	if err := env.ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	d, _ := env.disks.Get("disk-1")
	if d.Status.NodeName != "" {
		t.Fatalf("placement on a deleted node should be cleared, got %q", d.Status.NodeName)
	}
}
