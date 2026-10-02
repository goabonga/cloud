// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

type fakePuller struct {
	calls   []string // addr/uid pairs, joined, in call order
	pullErr error
	// write is copied into dest on a successful pull, so callers can assert
	// on-disk content/size afterward.
	write []byte
}

func (f *fakePuller) PullDisk(_ context.Context, addr, uid, dest string) error {
	f.calls = append(f.calls, addr+"/"+uid)
	if f.pullErr != nil {
		return f.pullErr
	}
	return os.WriteFile(dest, f.write, 0o600)
}

type asyncReplicaEnv struct {
	replicas *manager.AsyncDiskReplicaRegistry
	disks    *manager.DiskRegistry
	nodes    *registry.Registry[resource.NodeSpec, resource.NodeStatus]
	dir      string
}

func newAsyncReplicaEnv(t *testing.T) *asyncReplicaEnv {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	return &asyncReplicaEnv{
		replicas: registry.New[resource.AsyncDiskReplicaSpec, resource.AsyncDiskReplicaStatus](store, resource.KindAsyncDiskReplica),
		disks:    registry.New[resource.DiskSpec, resource.DiskStatus](store, resource.KindDisk),
		nodes:    registry.New[resource.NodeSpec, resource.NodeStatus](store, resource.KindNode),
		dir:      t.TempDir(),
	}
}

func (env *asyncReplicaEnv) reconciler(puller manager.DiskPuller, nodeName string) *manager.AsyncDiskReplicaReconciler {
	return manager.NewAsyncDiskReplicaReconciler(env.replicas, env.disks, env.nodes, puller, env.dir, "7332", nodeName)
}

func (env *asyncReplicaEnv) putNode(t *testing.T, uid, address string) {
	t.Helper()
	if err := env.nodes.Put(&resource.Node{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.NodeSpec{Hostname: uid, Address: address, Capacity: resource.NodeCapacity{CPUs: 1, MemoryMB: 1}},
	}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
}

func (env *asyncReplicaEnv) putDisk(t *testing.T, uid, nodeName string) {
	t.Helper()
	d := &resource.Disk{Metadata: resource.ObjectMeta{UID: uid, Generation: 1}, Spec: resource.DiskSpec{SizeMB: 1024}}
	d.Status.NodeName = nodeName
	if err := env.disks.Put(d); err != nil {
		t.Fatalf("seed disk: %v", err)
	}
}

func (env *asyncReplicaEnv) putReplica(t *testing.T, uid, diskID, nodeName string, intervalSeconds int, lastSyncedAt string) {
	t.Helper()
	r := &resource.AsyncDiskReplica{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.AsyncDiskReplicaSpec{DiskID: diskID, IntervalSeconds: intervalSeconds},
	}
	r.Status.NodeName = nodeName
	r.Status.LastSyncedAt = lastSyncedAt
	if err := env.replicas.Put(r); err != nil {
		t.Fatalf("seed replica: %v", err)
	}
}

func TestAsyncDiskReplicaWaitsForMissingDisk(t *testing.T) {
	t.Parallel()

	env := newAsyncReplicaEnv(t)
	env.putReplica(t, "replica-1", "disk-missing", "node-secondary", 0, "")
	rec := env.reconciler(&fakePuller{}, "node-secondary")

	if err := rec.Reconcile(context.Background(), "replica-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _ := env.replicas.Get("replica-1")
	if got.Status.Phase != resource.PhasePending {
		t.Fatalf("phase = %q, want Pending", got.Status.Phase)
	}
}

func TestAsyncDiskReplicaWaitsForUnplacedDisk(t *testing.T) {
	t.Parallel()

	env := newAsyncReplicaEnv(t)
	env.putDisk(t, "disk-1", "") // no primary node yet
	env.putReplica(t, "replica-1", "disk-1", "node-secondary", 0, "")
	puller := &fakePuller{}
	rec := env.reconciler(puller, "node-secondary")

	if err := rec.Reconcile(context.Background(), "replica-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(puller.calls) != 0 {
		t.Fatalf("should not pull before the disk is placed, got calls=%v", puller.calls)
	}
	got, _ := env.replicas.Get("replica-1")
	if got.Status.Phase != resource.PhasePending {
		t.Fatalf("phase = %q, want Pending", got.Status.Phase)
	}
}

func TestAsyncDiskReplicaSyncsFromThePrimary(t *testing.T) {
	t.Parallel()

	env := newAsyncReplicaEnv(t)
	env.putNode(t, "node-primary", "10.0.0.1")
	env.putDisk(t, "disk-1", "node-primary")
	env.putReplica(t, "replica-1", "disk-1", "node-secondary", 0, "")
	puller := &fakePuller{write: []byte("disk bytes")}
	rec := env.reconciler(puller, "node-secondary")

	if err := rec.Reconcile(context.Background(), "replica-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(puller.calls) != 1 || puller.calls[0] != "10.0.0.1:7332/disk-1" {
		t.Fatalf("expected one pull from the primary's replication address, got %v", puller.calls)
	}
	got, _ := env.replicas.Get("replica-1")
	if !got.Status.IsReady() {
		t.Fatalf("expected Ready, got phase=%q", got.Status.Phase)
	}
	if got.Status.BytesSynced != int64(len("disk bytes")) {
		t.Fatalf("bytesSynced = %d", got.Status.BytesSynced)
	}
	if got.Status.LastSyncedAt == "" {
		t.Fatal("expected lastSyncedAt to be set")
	}
	data, err := os.ReadFile(filepath.Join(env.dir, "replica-1.img"))
	if err != nil {
		t.Fatalf("read replica copy: %v", err)
	}
	if string(data) != "disk bytes" {
		t.Fatalf("replica copy content = %q", data)
	}
}

func TestAsyncDiskReplicaSkipsAPassBeforeItIsDue(t *testing.T) {
	t.Parallel()

	env := newAsyncReplicaEnv(t)
	env.putNode(t, "node-primary", "10.0.0.1")
	env.putDisk(t, "disk-1", "node-primary")
	// Synced a moment ago, with a long interval: not due again yet.
	env.putReplica(t, "replica-1", "disk-1", "node-secondary", 3600, time.Now().Format(time.RFC3339))
	puller := &fakePuller{write: []byte("x")}
	rec := env.reconciler(puller, "node-secondary")

	if err := rec.Reconcile(context.Background(), "replica-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(puller.calls) != 0 {
		t.Fatalf("should not have pulled before the interval elapsed, got %v", puller.calls)
	}
}

func TestAsyncDiskReplicaSyncsAgainOnceDue(t *testing.T) {
	t.Parallel()

	env := newAsyncReplicaEnv(t)
	env.putNode(t, "node-primary", "10.0.0.1")
	env.putDisk(t, "disk-1", "node-primary")
	// Synced an hour ago, with a short interval: due again.
	env.putReplica(t, "replica-1", "disk-1", "node-secondary", 60, time.Now().Add(-time.Hour).Format(time.RFC3339))
	puller := &fakePuller{write: []byte("fresh bytes")}
	rec := env.reconciler(puller, "node-secondary")

	if err := rec.Reconcile(context.Background(), "replica-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(puller.calls) != 1 {
		t.Fatalf("expected a pull once the interval elapsed, got %v", puller.calls)
	}
}

func TestAsyncDiskReplicaPullFailureSetsError(t *testing.T) {
	t.Parallel()

	env := newAsyncReplicaEnv(t)
	env.putNode(t, "node-primary", "10.0.0.1")
	env.putDisk(t, "disk-1", "node-primary")
	env.putReplica(t, "replica-1", "disk-1", "node-secondary", 0, "")
	puller := &fakePuller{pullErr: errors.New("connection refused")}
	rec := env.reconciler(puller, "node-secondary")

	if err := rec.Reconcile(context.Background(), "replica-1"); err == nil {
		t.Fatal("expected the pull error to surface")
	}
	got, _ := env.replicas.Get("replica-1")
	if got.Status.Phase != resource.PhaseError {
		t.Fatalf("phase = %q, want Error", got.Status.Phase)
	}
}

func TestAsyncDiskReplicaNodeScoping(t *testing.T) {
	t.Parallel()

	env := newAsyncReplicaEnv(t)
	env.putNode(t, "node-primary", "10.0.0.1")
	env.putDisk(t, "disk-1", "node-primary")
	env.putReplica(t, "replica-mine", "disk-1", "node-a", 0, "")
	env.putReplica(t, "replica-other", "disk-1", "node-b", 0, "")
	puller := &fakePuller{write: []byte("x")}
	rec := env.reconciler(puller, "node-a")

	for _, uid := range []string{"replica-mine", "replica-other"} {
		if err := rec.Reconcile(context.Background(), uid); err != nil {
			t.Fatalf("reconcile %s: %v", uid, err)
		}
	}
	if len(puller.calls) != 1 {
		t.Fatalf("only the replica scheduled to node-a should sync, got %v", puller.calls)
	}
	if got, _ := env.replicas.Get("replica-other"); got.Status.IsReady() {
		t.Fatal("replica on another node should be left alone")
	}
}

func TestAsyncDiskReplicaFinalize(t *testing.T) {
	t.Parallel()

	env := newAsyncReplicaEnv(t)
	env.putNode(t, "node-primary", "10.0.0.1")
	env.putDisk(t, "disk-1", "node-primary")
	env.putReplica(t, "replica-1", "disk-1", "node-secondary", 0, "")
	puller := &fakePuller{write: []byte("x")}
	rec := env.reconciler(puller, "node-secondary")
	if err := rec.Reconcile(context.Background(), "replica-1"); err != nil {
		t.Fatalf("reconcile up: %v", err)
	}
	copyPath := filepath.Join(env.dir, "replica-1.img")
	if _, err := os.Stat(copyPath); err != nil {
		t.Fatalf("expected a local copy before deletion: %v", err)
	}

	cur, _ := env.replicas.Get("replica-1")
	now := time.Now()
	cur.Metadata.DeletionTimestamp = &now
	if err := env.replicas.Put(cur); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}
	if err := rec.Reconcile(context.Background(), "replica-1"); err != nil {
		t.Fatalf("reconcile delete: %v", err)
	}
	if _, err := env.replicas.Get("replica-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected record removed, got %v", err)
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatalf("expected the local copy to be removed, err=%v", err)
	}
	if rec.Name() != resource.KindAsyncDiskReplica {
		t.Fatalf("name = %q", rec.Name())
	}
}
