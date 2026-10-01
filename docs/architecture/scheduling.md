# Scheduling

Compute instances are placed onto nodes by the **scheduler**, a cluster-level
controller in `infra-controller-manager`. It runs under leader election, so a
single instance is active across the cluster. Micro-VMs are placed by a
second, separate **microvm-scheduler** controller with the identical
algorithm below, substituting `microvm.spec.vcpus` for `compute.spec.cpu`;
see [MicroVMs and compute share nodes, not capacity accounting](#microvms-and-compute-share-nodes-not-capacity-accounting).

## Nodes and pools

- A **node** registers a host with the control plane along with its schedulable
  `capacity` (CPUs, memory, max pods) and `labels`.
- A **node pool** groups nodes by a label `nodeSelector` and reports how many
  members are ready.

## Liveness

A node is schedulable only while it is **recently seen**. The agent stamps its
node's `status.lastSeen` each pass (see [node-scoped realization](#node-scoped-realization)),
and the scheduler marks a node `Ready` only when that heartbeat is within its
ready window (four reconcile intervals). A node that has never heartbeated, or
whose heartbeat is stale, is marked `Pending` and excluded from placement; the
node pool's ready count tracks only live nodes.

When a node goes stale or is deleted, the scheduler **evicts** the compute placed
on it - clearing `status.nodeName` so it is rescheduled onto a live node the same
pass. (A still-running but unreachable node's workload is not stopped; eviction
only re-places the record.)

## Placement

Each reconcile pass the scheduler:

1. Recomputes every node's allocation from the compute already assigned to it
   (CPU, memory and pod counts), so allocation never drifts.
2. Derives each node's readiness from its heartbeat.
3. For each unscheduled compute (`status.nodeName` empty), selects the
   least-loaded **ready** node that
   - matches the compute's node pool selector (when `nodePoolId` is set), and
   - has free CPU, memory and pod capacity for the request.
4. Writes the chosen node to the compute's `status.nodeName`, and records the
   node's `status.allocated`/readiness and the pool's ready/total counts.

A compute that fits no node is left unscheduled and retried on the next pass.
Placement is written to the latest copy of the compute, so the agent's status
fields are preserved.

```
compute (nodePoolId?) ----> scheduler ----> status.nodeName = node-x
node-x.status.allocated += {cpu, memory, 1 pod}
```

## MicroVMs and compute share nodes, not capacity accounting

`microvm-scheduler` runs the same placement pass as `scheduler` - same
liveness rule, same eviction, same least-loaded/pool-selector pick - against
the micro-VM store instead of the compute one, reusing `vcpus` as the CPU
request. It is a separate controller, not a generalization of `scheduler`,
because the two resources have unrelated spec/status types.

Being separate means each one recomputes a node's `status.allocated` from
only its own kind's instances: whichever controller reconciles last
overwrites the other's contribution to that reported field, and each one's
own placement decision only ever sees its own kind's load on a node - never
the other's. A node pool that schedules both compute and microvm can
therefore be oversubscribed across the two kinds. Until capacity accounting
is made aware of both together, keep compute and microvm in disjoint node
pools (`nodePoolId`) if they might otherwise land on the same nodes.

## Node-scoped realization

The agent realizes compute (and, identically, micro-VMs) only where it was
placed. Set `GOA_NODE_ID` on `infra-agent` to the node's id and the agent
both heartbeats that node and reconciles only the compute and micro-VMs whose
`status.nodeName` matches; everything else is left to the node that owns it.

With `GOA_NODE_ID` unset the agent realizes every compute and micro-VM, which
is the single-host development default.

## Function instances

A [function](faas.md)'s warm-pool slots are realized as ordinary compute
instances, created by the function controller rather than a client. They carry
no special treatment here: the scheduler places them, and the agent reconciles
them, exactly like any user-created compute.
