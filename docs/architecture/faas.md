# FaaS functions

Functions realize a Lambda-like invoke model entirely on top of the existing
`compute` resource: there is no new isolation primitive, and a function's warm
pool is just ordinary compute instances a controller creates and retires.
Triggers beyond the synchronous HTTP path below (cron, event-driven) are not
built yet (see [Not yet built](#not-yet-built)).

## Function and function instance

- **Function** (`function`) - the shape run for each invocation: image,
  command, env, sizing, subnet/security-group/node-pool attachment (the same
  fields as a compute instance), the `port` its runtime listens on, plus a
  `warmPool` policy.
- **Function instance** (`function_instance`) - one pool slot, joining a
  function to the compute instance realizing it, with the host `port`
  allocated for it and the `nodeName` it landed on once scheduled. Created
  and owned by the function controller; never written directly by a client.

## Warm pool policy

```
warmPool:
  minWarm: 0             # floor kept running regardless of idle time
  maxWarm: 0             # 0 = unbounded; a hard cap that always wins over minWarm
  idleTtlSeconds: 0      # how long an instance above minWarm may sit idle
  allowColdStart: false  # permit a fresh instance on invoke when none are warm
```

Different combinations express different service tiers without a schema
change:

- `minWarm > 0`, `allowColdStart` false: "always warm" - never cold, never
  scales to zero.
- `minWarm` 0, `allowColdStart` true: "cold/cheap" - no idle cost, pays the
  creation latency on every invoke.
- `minWarm > 0`, `maxWarm > minWarm`, `allowColdStart` true: elastic burst.

Billing itself is out of scope; the policy shape only leaves room for it.

## Ready semantics

A function reaches `Ready` once its subnet, VPC and (if set) security group
resolve - the same checks a compute instance's own reconcile pass performs -
independent of `status.warmCount`. A function can be `Ready` with zero warm
instances, the same way a deployment is available independent of any one
replica's state.

## Pool reconciliation

The function controller is a cluster-level controller, run under the same
leader lease as the scheduler, because pool sizing is a cluster-wide decision
independent of placement. Each pass, per function:

1. Resolve dependencies; `Pending` until they do.
2. Sync each instance's phase from its backing compute.
3. Create compute + function-instance pairs until `minWarm` pending-or-live
   slots exist. Placement of these computes is left entirely to the
   [scheduler](scheduling.md), which treats them like any other compute.
4. Enforce `maxWarm` (evicting the oldest-idle first), then evict instances
   beyond `minWarm`'s most-recently-used floor once idle past
   `idleTtlSeconds`.
5. Record `status.warmCount` as the number of Warm instances backed by a
   Ready compute.

Deleting a function marks every instance - and in turn their computes - for
deletion, and only removes the function's own record once none remain.

```
function (warmPool) ----> function controller ----> function_instance (Warm)
                                                            |
                                                            v
                                                   compute (same scheduler,
                                                   same reconcile path as any
                                                   user-created instance)
```

## Invoking a function

`POST /function/{uid}/invoke` with a request body invokes the function
synchronously and streams the response back. The call:

1. Lists the function's instances and tries to atomically claim one that is
   `Warm` and backed by a `Ready` compute (a compare-and-swap to `Assigned`,
   via `Registry.TryUpdate`; a lost race - another invoke, or the pool
   controller's own resync - just moves on to the next candidate).
2. If none are claimable and `allowColdStart` is true, creates a fresh
   compute + function instance directly in `Assigned` state (the same shape
   `createInstance` builds, bypassing the pool) and waits for it to become
   `Ready`, bounded by a cold-start timeout. If `allowColdStart` is false,
   the call fails immediately rather than queuing.
3. Proxies the request to `http://<node address>:<instance port>/` and
   streams the response back.
4. Releases the instance to `Warm` regardless of outcome. A cold-started
   instance becomes a normal pool member at that point - with `minWarm: 0`
   and the default `idleTtlSeconds: 0`, the pool controller's very next pass
   evicts it, so a "cold/cheap" tier does not accumulate instances; a tier
   with a longer TTL keeps it warm for a while, per its own policy.

An instance is reached on an allocated host port, DNAT'd to the function's
`port` inside it, using the exact same mechanism a user-created compute's
port mapping already uses - no new networking was added for this.

**Operational prerequisite**: this is the first thing in the codebase that
dials `Node.Spec.Address` for real traffic (every other use is Terraform
state or a load balancer's own VIP, never an actual connection). It must be
an address the `infra-api` process can reach, not just a display string, for
invoke to work.

Enforcing `maxWarm` immediately after a cold start (rather than waiting for
the pool controller's next pass) is not implemented: a burst of cold starts
can transiently exceed the cap by a few instances for up to one reconcile
interval. This is the same kind of one-tick convergence delay the pool
controller already accepts elsewhere (e.g. finalizing a deleted function),
not a new class of bug.

## Not yet built

HTTP-routed triggers (path/host-based, API-Gateway style - today's invoke
endpoint must be called directly by UID), cron triggers, and event-driven
(pub/sub) triggers are later work, not part of this foundation.
