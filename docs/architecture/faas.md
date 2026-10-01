# FaaS functions

Functions realize a Lambda-like invoke model entirely on top of the existing
`compute` resource: there is no new isolation primitive, and a function's warm
pool is just ordinary compute instances a controller creates and retires.
Invoking a function is not implemented yet; this PR only lays the pool
foundation (see [Not yet built](#not-yet-built)).

## Function and function instance

- **Function** (`function`) - the shape run for each invocation: image,
  command, env, sizing and subnet/security-group/node-pool attachment, the
  same fields as a compute instance, plus a `warmPool` policy.
- **Function instance** (`function_instance`) - one pool slot, joining a
  function to the compute instance realizing it. Created and owned by the
  function controller; never written directly by a client.

## Warm pool policy

```
warmPool:
  minWarm: 0             # floor kept running regardless of idle time
  maxWarm: 0             # 0 = unbounded; a hard cap that always wins over minWarm
  idleTtlSeconds: 0      # how long an instance above minWarm may sit idle
  allowColdStart: false  # not consumed yet; reserved for the invoke path
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

## Not yet built

Invoking a function (the synchronous `POST /function/{uid}/invoke` path),
`allowColdStart`'s actual cold-start behaviour, and HTTP/cron/event-driven
triggers are later work, not part of this foundation.
