// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "fmt"

// KindAsyncDiskReplica is the resource kind for a periodic, best-effort
// copy of a disk's backing file onto a second node.
const KindAsyncDiskReplica = "async_disk_replica"

// AsyncDiskReplicaFinalizer is attached by the agent so the local copy is
// removed before the replica record is deleted.
const AsyncDiskReplicaFinalizer = "infra.io/async_disk_replica"

// Failover policy modes. See FailoverPolicy.
const (
	FailoverModeOptimistic = "optimistic"
	FailoverModeConfirmed  = "confirmed"
)

// FailoverPolicy configures how a failover controller decides a replica's
// primary is really gone before promoting this replica onto it. Optimistic
// matches the scheduler's existing compute-eviction behavior: promote the
// moment the primary's heartbeat goes stale. Confirmed additionally probes
// the primary directly over the replication transport first, promoting only
// if that also fails - strictly safer, never a guarantee against split
// brain (see docs/architecture/disk-replication.md).
//
// The default is Confirmed, not Optimistic: unlike a stateless compute,
// promoting a disk replica discards whatever the old primary has that
// hasn't synced yet, so the safer default costs nothing a cluster that
// wants speed can't opt out of by setting Mode explicitly.
type FailoverPolicy struct {
	Mode string `json:"mode,omitempty"`
}

// Validate reports whether the policy is well-formed.
func (p FailoverPolicy) Validate() error {
	switch p.Mode {
	case "", FailoverModeOptimistic, FailoverModeConfirmed:
		return nil
	default:
		return fmt.Errorf("failoverPolicy: mode must be %q or %q, got %q", FailoverModeOptimistic, FailoverModeConfirmed, p.Mode)
	}
}

// EffectiveMode returns the policy's mode, defaulting to Confirmed.
func (p FailoverPolicy) EffectiveMode() string {
	if p.Mode == "" {
		return FailoverModeConfirmed
	}
	return p.Mode
}

// DefaultAsyncReplicaIntervalSeconds is used when IntervalSeconds is unset.
const DefaultAsyncReplicaIntervalSeconds = 60

// AsyncDiskReplicaSpec is the desired state of a periodic disk replica.
type AsyncDiskReplicaSpec struct {
	// DiskID is the disk to replicate.
	DiskID string `json:"diskId"`
	// TargetNodePoolID constrains the replica's placement to a node pool;
	// empty schedules onto any ready node other than the disk's primary.
	TargetNodePoolID string `json:"targetNodePoolId,omitempty"`
	// IntervalSeconds is how often the replica pulls the disk's current
	// backing file. Zero uses DefaultAsyncReplicaIntervalSeconds.
	IntervalSeconds int            `json:"intervalSeconds,omitempty"`
	FailoverPolicy  FailoverPolicy `json:"failoverPolicy,omitempty"`
}

// Validate reports whether the spec is well-formed.
func (s AsyncDiskReplicaSpec) Validate() error {
	if s.DiskID == "" {
		return fmt.Errorf("async_disk_replica: diskId is required")
	}
	if s.IntervalSeconds < 0 {
		return fmt.Errorf("async_disk_replica: intervalSeconds must not be negative")
	}
	return s.FailoverPolicy.Validate()
}

// IntervalOrDefault returns IntervalSeconds, or
// DefaultAsyncReplicaIntervalSeconds when unset.
func (s AsyncDiskReplicaSpec) IntervalOrDefault() int {
	if s.IntervalSeconds == 0 {
		return DefaultAsyncReplicaIntervalSeconds
	}
	return s.IntervalSeconds
}

// AsyncDiskReplicaStatus is the observed state of a periodic disk replica.
type AsyncDiskReplicaStatus struct {
	StatusBase
	// NodeName is the node holding this replica's copy.
	NodeName string `json:"nodeName,omitempty"`
	// LastSyncedAt is when the copy last completed, RFC3339.
	LastSyncedAt string `json:"lastSyncedAt,omitempty"`
	// BytesSynced is the size of the copy as of LastSyncedAt.
	BytesSynced int64 `json:"bytesSynced,omitempty"`
}

// AsyncDiskReplica is a periodic, best-effort disk-replica resource.
type AsyncDiskReplica = Resource[AsyncDiskReplicaSpec, AsyncDiskReplicaStatus]
