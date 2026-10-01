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

## What's not here yet

The transport has no consumer yet: there is no resource kind that uses it to
actually keep a replica in sync, and no automatic failover. Those land in
later changes on top of this one.
