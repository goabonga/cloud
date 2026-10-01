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

VPC, subnet, internet gateway, route, peering, security group (+ rule), IP
address, compute, microvm, disk, disk file, DNS zone, DNS record, KMS
keyring, KMS key, secret (+ version), SSL CA, SSL cert, WAF policy (+ rule),
ACL policy (+ rule), load balancer (+ backend), load balancer target group
(+ target) and listener.

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
