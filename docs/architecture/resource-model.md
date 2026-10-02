# Resource model

Every resource is a record with three parts, following the Kubernetes
convention:

- **metadata** - identity and lifecycle: `uid`, `name`, `organizationId`,
  `projectId`, `labels`, `annotations`, `ownerRefs`, `finalizers`,
  `generation`, `deletionTimestamp`, `createdAt`.
- **spec** - the desired state declared by the client.
- **status** - the observed state recorded by the controllers and agent:
  `phase`, `conditions` and `observedGeneration`.

## Phases

```
Pending -> Reconciling -> Ready
                       -> Error
           Deleting    -> Terminated
```

## Conditions

Conditions report orthogonal facts about a resource, for example `Ready`,
`Synced`, `Healthy`, `Progressing`, `Degraded`, `Scheduled` and `Bound`. Each
carries a status, reason and timestamp.

## Ownership and cleanup

`ownerRefs` express parent/child relationships so deleting a parent cascades to
its children. `finalizers` block deletion until a controller has released the
underlying kernel resources.

## Resource types

VPC, subnet, internet gateway (with an optional egress proxy: allowed domains
and addresses, see [realization](realization.md#egress-proxy)), route,
peering, security group (+ rule), IP address, compute, microvm, disk, disk
file, async disk replica, DNS zone, DNS record, KMS keyring, KMS key, secret
(+ version), SSL CA, SSL cert, WAF policy (+ rule), ACL policy (+ rule),
load balancer (+ backend), load balancer target group (+ target), listener,
organization, folder, project, IAM binding, function and function instance
(see [FaaS functions](faas.md)).

## Defaults

A spec type may fill its own defaults (`Defaulter`). The API applies them
before validating a write, so the stored spec - and what a client reads back -
holds the effective values rather than empty fields.

## Layer-7 load balancing

A load balancer's `backend`s balance one port at layer 4. Listeners, target
groups and targets describe layer-7 balancing on top of it:

- **Target group** (`lb_target_group`) - a pool of targets in a VPC, with the
  protocol towards them (`http` by default, `https` or `tcp`), their port, and
  a health check (protocol, path, port, interval, timeout and thresholds, each
  with a default). With `https`, `backendCaId` names the CA verifying the
  targets' certificates - empty, the platform's global CAs - and `serverName`
  the name they are verified against and asked for by SNI - empty, the
  request's host. Its status lists each target's health as reported by
  agents.
- **Target** (`lb_target`) - a compute instance in a group, with an optional
  port of its own and a weight (1 to 1000, 1 by default).
- **Listener** (`lb_listener`) - a port of a load balancer and its protocol:
  - `http`, routed by host and path prefix;
  - `https`, terminating TLS with its `certificateIds` (picked by SNI) and
    speaking HTTP to the targets (`terminate`, the default) or new TLS
    connections (`reencrypt`);
  - `tls`, passing TLS through untouched and routing on the SNI host only
    (`passthrough`): the targets hold the certificates;
  - `tcp`, forwarding connections as they are, without rules.

  Requests no rule matches go to `defaultTargetGroupId`.

A listener serves its load balancer's VIP and public address. A load balancer
serving only listeners leaves its `port` unset: it then gets no layer-4
virtual service, and a listener on the load balancer's own port is refused.
The agents serve the listeners with infra-lb (see
[realization](realization.md#listeners)).

## Organization, folder and project hierarchy

Resources are scoped for isolation, IAM, quotas and billing by
`metadata.projectId`. A **project** (`project`) attaches directly to an
**organization** (`organization`) or to a **folder** (`folder`), and a folder
may itself nest under an organization or another folder - an organization is
always the root, never nested.

An **IAM binding** (`iam_binding`) grants a fixed role (`roles/viewer`,
`roles/editor` or `roles/owner`) to a set of `"<kind>:<id>"` members (for
example `user:alice`) on a resource: an organization, a folder, a project, or
any other resource for one-off sharing. A binding on an organization or
folder is meant to be inherited by every project (and its resources)
underneath it.

When the API is started with authentication enabled, the hierarchy and
bindings are enforced on every resource kind except `user` and
`access_token` (which keep their own admin-only gate) and `secret`/`ssl`
(not yet covered): a caller may read or write a resource only if they
created it (`metadata.ownerUid`, stamped by the API and never
client-settable), or hold a binding - direct or inherited through the
resource's project's folder/organization chain - granting the needed
permission, or carry the global `admin` role. `list` only returns what the
caller can read. A resource with no `metadata.projectId` has no chain to
inherit from, so only its owner or an admin can reach it; creating one is
always allowed regardless, and the creator becomes its owner. A resource's
`metadata.projectId` is fixed at creation - a write that tries to change it
is rejected. `iam_binding` itself is admin-only for every verb, with no
self-service path: since a binding is what grants access, letting its
creator manage it the same way would let any caller grant themselves a role
on a project they otherwise can't touch.

Without authentication enabled, nothing above applies and every kind is as
open as it was before this model existed - the same posture every other
unauthenticated deployment of this API already has.

See [Identity and access management](iam.md) for the full permission
vocabulary, the global `admin` role, and how a caller authenticates in the
first place.

## HTTP request limits

The control-plane API and IdP accept request bodies up to 1 MiB, including
chunked requests. Larger bodies return HTTP 413 before authentication or routing.
Each server instance admits at most 64 concurrent requests; saturation returns
HTTP 503. A connection-address token bucket allows 100 requests/second with a
burst of 200; exceeding it returns HTTP 429 and `Retry-After: 1`. Peer bookkeeping
is bounded to 4096 entries and expired after ten idle minutes. Forwarding headers
are not trusted: behind a proxy, its connected address shares a bucket.

Listeners allow ten seconds for headers, thirty seconds for request reads, two
minutes for response writes, sixty seconds for idle keep-alive, and 32 KiB of
headers. Request contexts expire after two minutes. Health/readiness probes bypass
admission throttles but retain listener timeouts. Function invocation inputs are
also subject to the body limit; long streaming responses are bounded by the write
timeout. Limits are process-local, not a distributed tenant quota.

## Collection pagination

Collection GETs return up to 100 resources by default. Set `limit=1..1000` to
choose a page size, then pass the response's opaque `continue` token in the next
GET. Items are ordered by UID. Authorization filtering and secret redaction happen
before page selection; cursors reference only visible resources. Tokens identify
a position, not a frozen snapshot, so resources inserted before that position
while iterating appear on the next full traversal.

The Go client, Terraform provider and dashboard automatically follow continuation
pages. Raw HTTP consumers must do the same to retrieve an entire collection.
Invalid limits and tokens return HTTP 400. IAM binding and hierarchy reads are
cached only within an individual collection request, with decisions separated by
subject, project and permission; the next request observes revoked grants.
Pagination bounds response item counts. Storage backends still materialize the
collection before filtering, so it does not provide storage-level streaming or a
bound on memory proportional to the total stored resource count.


## Runtime admission limits

Computes and function instances default to one CPU, 256 MiB of memory and 256
processes when sizing values are omitted or zero. Accepted explicit shapes range
from 0.01 to 64 CPUs, up to 262144 MiB of memory and at most 4096 processes;
negative and non-finite values are rejected. micro-VMs require positive explicit
CPU/memory values and share the 64 CPU/262144 MiB ceilings. A single disk is limited
to 1048576 MiB (1 TiB).

Warm-pool `minWarm` and `maxWarm` must not exceed 256. Omitted/zero `maxWarm`
defaults to `max(32, minWarm)`. Cold starts retain their existing policy semantics
and may exceed this warm-pool target; this is not an aggregate instance quota.

Defaults also apply to legacy computes at realization and in scheduler accounting,
so omitted requests no longer reserve zero capacity while consuming unrestricted
host resources. Changed effective sizing can restart those workloads. The function
controller refuses oversized legacy pools, and disk/VM realization refuses shapes
above the ceilings. Delete/finalizer paths remain available for cleanup.

Terraform validates explicit positive sizing and warm-pool caps before apply; omit
these attributes to use defaults. Existing Terraform configuration using explicit
zero sizing or `max_warm = 0` must remove those values. Configurations above the
ceilings must be resized before updating. These are static per-resource limits;
distributed aggregate project budgets are not implemented by this change.
