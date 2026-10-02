// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// DiskPuller abstracts pulling a disk's current backing file from the node
// that owns it. *replication.Client satisfies this; manager does not import
// replication directly so its tests can fake the transport.
type DiskPuller interface {
	PullDisk(ctx context.Context, addr, uid, dest string) error
}

// AsyncDiskReplicaRegistry is the typed store the async-replica reconciler
// reads and writes.
type AsyncDiskReplicaRegistry = registry.Registry[resource.AsyncDiskReplicaSpec, resource.AsyncDiskReplicaStatus]

// AsyncDiskReplicaReconciler keeps a periodic, best-effort local copy of a
// disk's backing file up to date by pulling it from the disk's primary node
// over the replication transport. The copy lives apart from the primary's
// own backing-file directory - see dir below - so nothing confuses it for
// the disk itself; promoting a replica is a later, separate step (the
// failover controller), not something this reconciler does.
type AsyncDiskReplicaReconciler struct {
	reg             *AsyncDiskReplicaRegistry
	disks           *DiskRegistry
	nodes           *registry.Registry[resource.NodeSpec, resource.NodeStatus]
	puller          DiskPuller
	dir             string
	replicationPort string
	nodeName        string
	now             func() time.Time
}

// NewAsyncDiskReplicaReconciler returns a reconciler storing copies under
// dir (typically <stateDir>/disk-replicas), pulling from a primary's
// address on replicationPort. nodeName scopes realization to replicas
// scheduled to this node; an empty nodeName realizes every replica.
func NewAsyncDiskReplicaReconciler(
	reg *AsyncDiskReplicaRegistry,
	disks *DiskRegistry,
	nodes *registry.Registry[resource.NodeSpec, resource.NodeStatus],
	puller DiskPuller,
	dir, replicationPort, nodeName string,
) *AsyncDiskReplicaReconciler {
	return &AsyncDiskReplicaReconciler{
		reg: reg, disks: disks, nodes: nodes, puller: puller,
		dir: dir, replicationPort: replicationPort, nodeName: nodeName,
		now: time.Now,
	}
}

// Name identifies the reconcile pass.
func (r *AsyncDiskReplicaReconciler) Name() string { return resource.KindAsyncDiskReplica }

func (r *AsyncDiskReplicaReconciler) localPath(uid string) string {
	return filepath.Join(r.dir, uid+".img")
}

// ReconcileAll reconciles every async disk replica, collecting per-replica
// errors.
func (r *AsyncDiskReplicaReconciler) ReconcileAll(ctx context.Context) error {
	replicas, err := r.reg.WithContext(ctx).List()
	if err != nil {
		return fmt.Errorf("manager: list async disk replicas: %w", err)
	}
	var errs []error
	for i := range replicas {
		uid := replicas[i].Metadata.UID
		if err := r.Reconcile(ctx, uid); err != nil {
			errs = append(errs, fmt.Errorf("async disk replica %s: %w", uid, err))
		}
	}
	return errors.Join(errs...)
}

// Reconcile brings the replica identified by uid in line with its spec.
func (r *AsyncDiskReplicaReconciler) Reconcile(ctx context.Context, uid string) error {
	rep, err := r.reg.WithContext(ctx).Get(uid)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("manager: load async disk replica %q: %w", uid, err)
	}
	if r.nodeName != "" && rep.Status.NodeName != r.nodeName {
		// Scheduled to another node (or not yet scheduled); leave it alone.
		return nil
	}
	if rep.Metadata.IsDeleting() {
		return r.finalize(ctx, rep)
	}
	return r.ensure(ctx, rep)
}

func (r *AsyncDiskReplicaReconciler) ensure(ctx context.Context, rep *resource.AsyncDiskReplica) error {
	if !rep.Metadata.HasFinalizer(resource.AsyncDiskReplicaFinalizer) {
		rep.Metadata.AddFinalizer(resource.AsyncDiskReplicaFinalizer)
	}

	disk, err := r.disks.Get(rep.Spec.DiskID)
	if errors.Is(err, state.ErrNotFound) {
		rep.Status.SetPhase(resource.PhasePending, "WaitingForDisk", "disk "+rep.Spec.DiskID+" does not exist")
		return r.reg.WithContext(ctx).Put(rep)
	}
	if err != nil {
		return fmt.Errorf("manager: load disk %q: %w", rep.Spec.DiskID, err)
	}
	if disk.Status.NodeName == "" {
		rep.Status.SetPhase(resource.PhasePending, "WaitingForDisk", "disk has no primary node yet")
		return r.reg.WithContext(ctx).Put(rep)
	}

	if !r.dueToSync(rep) {
		return nil // not time yet; this pass is a no-op
	}

	node, err := r.nodes.Get(disk.Status.NodeName)
	if err != nil {
		rep.Status.SetPhase(resource.PhaseError, "PrimaryNodeMissing", err.Error())
		_ = r.reg.WithContext(ctx).Put(rep)
		return err
	}
	addr := net.JoinHostPort(node.Spec.Address, r.replicationPort)

	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		rep.Status.SetPhase(resource.PhaseError, "SyncError", err.Error())
		_ = r.reg.WithContext(ctx).Put(rep)
		return fmt.Errorf("manager: replica dir: %w", err)
	}
	dest := r.localPath(rep.Metadata.UID)
	if err := r.puller.PullDisk(ctx, addr, disk.Metadata.UID, dest); err != nil {
		rep.Status.SetPhase(resource.PhaseError, "SyncError", err.Error())
		_ = r.reg.WithContext(ctx).Put(rep)
		return err
	}

	info, err := os.Stat(dest)
	if err != nil {
		rep.Status.SetPhase(resource.PhaseError, "SyncError", err.Error())
		_ = r.reg.WithContext(ctx).Put(rep)
		return err
	}
	rep.Status.LastSyncedAt = r.now().UTC().Format(time.RFC3339)
	rep.Status.BytesSynced = info.Size()
	rep.Status.MarkReconciled(rep.Metadata.Generation)
	rep.Status.SetPhase(resource.PhaseReady, "Synced", "replica up to date")
	return r.reg.WithContext(ctx).Put(rep)
}

// dueToSync reports whether enough time has passed since the last sync (or
// there has never been one) for another to run.
func (r *AsyncDiskReplicaReconciler) dueToSync(rep *resource.AsyncDiskReplica) bool {
	if rep.Status.LastSyncedAt == "" {
		return true
	}
	last, err := time.Parse(time.RFC3339, rep.Status.LastSyncedAt)
	if err != nil {
		return true
	}
	interval := time.Duration(rep.Spec.IntervalOrDefault()) * time.Second
	return r.now().Sub(last) >= interval
}

func (r *AsyncDiskReplicaReconciler) finalize(ctx context.Context, rep *resource.AsyncDiskReplica) error {
	if rep.Metadata.HasFinalizer(resource.AsyncDiskReplicaFinalizer) {
		if err := os.Remove(r.localPath(rep.Metadata.UID)); err != nil && !errors.Is(err, os.ErrNotExist) {
			rep.Status.SetPhase(resource.PhaseError, "DeleteError", err.Error())
			_ = r.reg.WithContext(ctx).Put(rep)
			return err
		}
		rep.Metadata.RemoveFinalizer(resource.AsyncDiskReplicaFinalizer)
		rep.Status.SetPhase(resource.PhaseDeleting, "Deleting", "replica removed")
		if err := r.reg.WithContext(ctx).Put(rep); err != nil {
			return fmt.Errorf("manager: save async disk replica %q: %w", rep.Metadata.UID, err)
		}
	}
	if len(rep.Metadata.Finalizers) == 0 {
		if err := r.reg.WithContext(ctx).Delete(rep.Metadata.UID); err != nil {
			return fmt.Errorf("manager: delete async disk replica %q: %w", rep.Metadata.UID, err)
		}
	}
	return nil
}
