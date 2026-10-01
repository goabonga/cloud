# Disk replication

A disk (`infra_disk`) is pinned to exactly one node (see
[scheduling.md](scheduling.md#disks-are-pinned-too-with-no-capacity-accounting)):
its backing file lives there and nowhere else. Losing that node loses the
data. This document describes the mechanism built to replicate a disk's
backing file to a second node and, eventually, fail a compute or micro-VM
over to the replica automatically.

## Transport

Before anything resource-shaped can replicate, two agents need a way to talk
to each other at all - today the only inter-node channel in the codebase is
the per-VPC VXLAN data plane, which carries tenant traffic exclusively and
has no hook for agent-originated control or bulk-data traffic (see
`internal/manager/overlay.go`). `internal/replication` is a new, dedicated
channel for this:

```
infra-agent (secondary)              infra-agent (primary)
      |  GET /disks/{uid}                    |
      |  signed, Range-resumable  ----------->|  serves <stateDir>/disks/<uid>.img
      |<----------------------- 200/206 ------|  via http.ServeContent
      |                                       |
      |  GET /ping (unauthenticated) -------->|  liveness probe
      |<----------------------- 200 -----------|
```

- **Server** (`replication.Server`, started by `infra-agent` on
  `GOA_REPLICATION_ADDR`, default `:7332`) serves a node's own disk backing
  files read-only, and answers `/ping`.
- **Client** (`replication.Client`) pulls a disk from another node, resuming
  a prior partial pull by requesting `Range: bytes=<have>-` and appending;
  the destination file is only ever renamed into place once the transfer is
  complete, so a crash mid-pull leaves a `.part` file to resume from, never
  a half-written disk.
- **Auth**: every request but `/ping` is signed with an HMAC-SHA256 over
  `method\npath\ntimestamp`, keyed by a subkey derived from the cluster's
  shared `GOA_KMS_KEY` (`crypto.DeriveKey(master, "replication:transport",
  32)`) - the same pre-shared material every node already holds, rather than
  a new node-identity/PKI system. A node without `GOA_KMS_KEY` configured
  runs the transport server off entirely rather than serve unauthenticated.
  This is plain HTTP, not HTTPS: the signature stops an unkeyed sender from
  pulling or probing anything, but the data itself is not encrypted in
  transit. mTLS off the existing `internal/ssl` root CA machinery is a
  possible later hardening, not required for this foundation.

## Async replica (`infra_async_disk_replica`)

The first, and simpler, of two replica kinds a disk can have attached (the
other, `infra_sync_disk_replica`, mirrors writes in real time and is covered
separately). Attaching nothing to a disk means no replication at all - this
is opt-in, not a default every disk pays for.

- **Scheduling** (`AsyncDiskReplicaSchedulerController`): placed on the
  least-loaded ready node that is explicitly **not** the disk's own current
  node, honouring `target_node_pool_id` when set. A replica whose disk has
  no placement yet, or references a missing pool, waits unscheduled rather
  than erroring - both resolve themselves once the disk (or pool) catches
  up.
- **Syncing** (`AsyncDiskReplicaReconciler`, on the replica's own node):
  every `interval_seconds` (default 60), pulls the disk's current backing
  file from its primary node's replication server (see Transport above)
  into its own copy, recording `last_synced_at` and `bytes_synced`. A v1
  full pull every interval, not an incremental block-diff - correctness
  over efficiency for the foundation; `replication.Client.PullDisk`'s resume
  support only covers a transfer interrupted mid-pull, not a diff against
  the previous copy.
- **Where the copy lives**: apart from the primary disk's own backing-file
  directory (`<stateDir>/disk-replicas/<replica-uid>.img`, not
  `<stateDir>/disks/<disk-uid>.img`). A replica becoming the new primary is
  a deliberate, separate step - the failover controller - not something
  this reconciler does by writing into the primary's path itself.
- **`failover_policy.mode`** (`optimistic` or `confirmed`, default
  `confirmed`): consumed by the failover controller, not by this
  reconciler - recorded here because it travels with the replica, the thing
  that gets promoted.

## What's not here yet

No automatic failover, and no sync replica kind yet - both land in later
changes on top of this one. Nothing promotes a replica onto the disk it
replicates: today, losing the primary node still loses the compute/micro-VM's
access to its data, but the replica itself keeps a recoverable copy a human
can act on.
