# infrastructure

A declarative, Linux-native cloud control plane. It provisions VPCs, compute,
networking, DNS, KMS, secrets, SSL, WAF, ACL and load balancers using only
Linux kernel primitives - network namespaces, bridges and VXLAN, iptables,
cgroups v2, dm-crypt and OCI images, with DNS served by the agent itself. There is no Docker daemon,
libvirt or OVS in the data path.

## How it fits together

Resources follow a Kubernetes-style `metadata` / `spec` / `status` model:

- **Clients** - the `infra` CLI and the `terraform-provider-infra` Terraform
  provider - submit desired state.
- **Control plane** - `infra-api` records spec and status;
  `infra-controller-manager` schedules and reconciles across hosts.
- **Data plane** - `infra-agent` runs on each host and reconciles the recorded
  spec against the kernel.
- **Identity and observability** - `infra-idp` issues identity; `infra-exporter`
  exposes Prometheus metrics.

## Where to go next

- [Getting started](guide/getting-started.md) - build and run the components.
- [Installation](guide/installation.md) - install on a host.
- [Architecture overview](architecture/overview.md) - control plane, data plane
  and state.
- [Resource model](architecture/resource-model.md) - spec, status, phases and
  conditions.
- [Terraform provider](provider/index.md) - manage resources as code.
- [Stability and deprecation](stability.md) - versioning policy.
