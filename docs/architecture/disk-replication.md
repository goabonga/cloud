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
      |  signed, Range-resumable  ----------->|  serves an immutable snapshot
      |<----------------------- 200/206 ------|  via http.ServeContent
      |                                       |
      |  GET /ping (unauthenticated) -------->|  liveness probe
      |<----------------------- 200 -----------|
```

- **Server** (`replication.Server`, started by `infra-agent` on
  `GOA_REPLICATION_ADDR`, default `:7332`) serves a node's own disk backing
  files through immutable snapshots, and answers `/ping`.
- **Client** (`replication.Client`) pulls a disk from another node, resuming
  a prior partial pull by requesting `Range: bytes=<have>-` for the stored
  snapshot digest. A SHA-256 checksum is verified over the entire completed
  file before replacing the previous replica. A `.part.version` sidecar pins
  the snapshot; legacy partials, expired snapshots and unsatisfiable ranges
  restart the transfer instead of mixing disk versions.
- **Transport authentication**: production listeners and clients use HTTPS with
  TLS 1.3 and mutual certificate verification. `GOA_MANAGEMENT_TLS_CA`,
  `GOA_MANAGEMENT_TLS_CERT` and `GOA_MANAGEMENT_TLS_KEY` point to a dedicated
  management CA and node identity. Certificates must include each advertised
  node address in their subject alternative names. This authority is independent
  of the tenant/public root; tenant-issued certificates cannot authenticate nodes.
  Even `/ping` requires a management certificate at the TLS layer. Redirects are
  rejected and peer names are verified; there is no plaintext downgrade.
- **Request authentication**: disk requests additionally retain the HMAC-SHA256
  signature derived from `GOA_KMS_KEY`. Nodes without that key do not start disk
  replication. With a KMS key, missing management credentials fail startup unless
  replication is explicitly disabled with `GOA_REPLICATION_DISABLED=1`.

See [deployment operations](../operations/deploy.md#management-transport-tls)
for provisioning, migration and certificate rotation.

## Snapshot requirements

The primary captures each transfer using the filesystem's atomic `FICLONE`
reflink operation. The disk backing directory must live on a filesystem that
implements this operation. Unsupported filesystems return HTTP 503 and the
replica reports a sync error; there is no fallback to copying a disk while it
is being modified. Existing installations on unsupported filesystems must
move the disk directory to a compatible volume before replication can work.

Each disk retains its latest content-addressed snapshot under
`<stateDir>/disks/.snapshots/<disk-uid>`. Existing transfers hold an immutable
file descriptor, while later resumes of an evicted version restart. The hash
and version identify one filesystem snapshot, not an application transaction:
these are crash-consistent snapshots, without guest/application quiescing.

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
