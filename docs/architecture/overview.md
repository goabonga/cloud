# Architecture overview

infrastructure separates a control plane from a data plane, mirroring the way
Kubernetes splits API/controllers from kubelets.

```
Clients                       Control plane                 Data plane
-------                       -------------                 ----------
terraform-provider-infra ->   infra-api (REST)        ->    infra-agent (per host)
infra (cli)              ->        |                              |
                                   v                              v
                              state store                   Linux kernel
                              (file | etcd | postgres)      (netns, bridges, VXLAN,
                                   ^                          iptables, cgroups v2,
infra-idp (identity)               |                          dm-crypt, DNS, OCI)
infra-exporter (metrics) <- controller-manager
                              (scheduler + reconcilers)
```

## Control plane

- **infra-api** records the desired `spec` and observed `status` of every
  resource and serves them over REST.
- **infra-controller-manager** schedules resources onto hosts and runs the
  cluster-level reconcile loops.

## Data plane

- **infra-agent** runs on each host, watches the resources assigned to it and
  reconciles them against the kernel. See [realization](realization.md) for how a
  declared topology becomes bridges, namespaces, cgroups and encrypted disks.

## State

The store is pluggable and selected through a single DSN
(`GOA_STATE_DSN` / `-state-dsn`): an `etcd://ep1,ep2,...` DSN uses etcd, any
other non-empty DSN uses PostgreSQL, and an empty DSN falls back to a local
file store. Both etcd and PostgreSQL are highly available, multi-instance
backends suitable for multi-host clusters (every component must share one);
the file backend is for single-host development. Every backend offers the same
contract, including an atomic compare-and-swap used for leader election.

See the [resource model](resource-model.md) for the spec/status contract.
