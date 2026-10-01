# Multi-host test deployment (libvirt)

Provision a small libvirt/KVM cluster, install the Debian packages on it and
start the services. Use this to test the `.deb` artifacts on real machines.

## Prerequisites

On the libvirt host:

- `libvirtd`, `virt-install`, `qemu-img` and `cloud-image-utils` (`cloud-localds`)
- An Ubuntu 24.04 (noble) cloud image at the path in `group_vars/all.yml`
  (`base_image`). The upstream `.img` is already a qcow2 and is BIOS-bootable,
  for example:

  ```bash
  curl -L -o /var/lib/libvirt/images/noble-server-cloudimg-amd64.img \
    https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img
  ```

- An SSH key at `~/.ssh/id_ed25519.pub` (or change `ssh_public_key`).

## Build the artifacts

From the repository root:

```bash
make deb VERSION=0.1.0   # .deb packages (incl. infra-www, SPA embedded) -> dist/
make build-provider      # Terraform provider                            -> build/terraform-provider-infra
```

Keep `version` in `group_vars/all.yml` in sync with the `VERSION` you build.

## Deploy the full stack

Run these as your normal user, not under `sudo`: the `ssh_public_key` lookup
reads `$HOME/.ssh/id_ed25519.pub`, and under `sudo` `$HOME` becomes `/root`.
`-K` (`--ask-become-pass`) prompts for the local sudo password that `become`
needs to drive `qemu:///system` and write into `images_dir`.

```bash
cd deploy/ansible

# 1. Create the VMs (control + two agents) on the libvirt default network.
ansible-playbook --ask-become-pass create-vms.yml

# 2. Deploy etcd, the control plane, the IdP, the dashboard, monitoring,
#    the Terraform tooling, the agents and the controller-managers.
ansible-playbook --ask-become-pass site.yml
```

`site.yml` first generates local credentials under `.credentials/` (gitignored):
the KMS key, the IdP ES256 keypair and a Terraform client secret. It then
deploys:

| Host | Component | Address | Notes |
| --- | --- | --- | --- |
| every host | etcd | `:2379` | one member per host; quorum survives one VM down |
| `infra-control` | infra-api | `:8080` | verifies IdP JWTs, secret/disk encryption enabled |
| `infra-control` | infra-idp | `:8081` | ES256 JWT issuer (client-credentials grant) |
| `infra-control` | infra-exporter | `:9100` | Prometheus metrics |
| `infra-control` | infra-www | `:8088` | dashboard (embedded SPA + API reverse proxy) |
| `infra-control` | Prometheus | `:9090` | scrapes the exporter (native, no Docker) |
| `infra-control` | Grafana | `:3000` | admin / infra; infra dashboard provisioned |
| `infra-control` | Terraform | - | local provider + demo workspace in `~/infra-demo` |
| `infra-agent-*` | infra-agent | - | registered as a schedulable node |
| `infra-agent-*` | infra-controller-manager | - | one leader via a lease in etcd, the other on standby |

The API and the IdP run as the unprivileged `infra` user; their PEM keys reach
them as systemd credentials (`LoadCredential=`), and a missing key stops the
service from starting rather than letting it run without authentication.

## Exercise the stack from the control VM

```bash
ssh ubuntu@192.168.122.10
infra-login          # fetch a JWT from the IdP into GOA_API_TOKEN
cd ~/infra-demo
terraform apply      # VPC, subnet, gateway, firewall, encrypted disk, compute
```

The workspace is already initialised against the provider installed in
`~/.terraform.d/plugins`. The `infra` CLI reads the same `GOA_API_URL` and
`GOA_API_TOKEN`. To run Terraform from the libvirt host instead, see
[terraform/README.md](terraform/README.md).

## Verify, and drill a failover

```bash
ansible-playbook verify.yml                   # checks only
ansible-playbook verify.yml -e failover=true  # + failover drill
```

The checks assert that the API refuses a call without a token and accepts an
IdP one, that both agents are registered nodes, that etcd has a healthy member
on every host and that an agent host holds the controller-manager lease.

The drill simulates the lease holder hanging rather than shutting down cleanly
(a clean stop releases the lease at once, which would not exercise expiry): it
freezes the holder's controller-manager with `systemctl freeze` and stops its
etcd member, waits for the other agent host to take the lease once the 15s TTL
runs out while the API keeps serving from the two remaining members, then
thaws the old holder and checks it stays on standby. Both services are
restored afterwards.

What it does not cover: there is no node heartbeat, so compute already placed
on a stopped agent host stays assigned to it rather than being rescheduled. And
the lease carries no fencing token, so a holder that hangs mid-reconcile can
finish that pass after it thaws, before its next lease check demotes it.

## Simulated provider (upstream-sim)

A fourth VM, `upstream-sim` (`192.168.122.30`), plays the provider's router.
It and both agents - which double as the edges - are attached to `wan-sim`, an
isolated libvirt network standing in for the transit link:

```
[inet netns] -- [upstream-sim .100] -- wan-sim 10.99.0.0/24 -- [infra-agent-1 .1]
 100.64.0.2       100.64.0.1                                  [infra-agent-2 .2]
```

- **Public block**: `203.0.113.0/24` (TEST-NET-3, RFC 5737), never routed on
  the Internet, so it cannot shadow a real address. `upstream.route_mode` in
  `group_vars/all.yml` picks how upstream-sim routes it:
  - `bgp` (default): BIRD on upstream-sim (AS `upstream.asn`) peers with each
    edge (AS `edge_asn`) over BGP with BFD, and installs every public address
    an edge announces as a /32, with all the edges announcing it as equal next
    hops and flows spread over them by an L4 hash. The rest of the block is
    unreachable. An edge's routes leave with it: within a second of its going
    silent, at once when its agent stops. `sudo birdc show protocols` and
    `ip route show 203.0.113.0/24 root 203.0.113.0/24` on upstream-sim show
    the sessions and routes.
  - `ecmp`: the whole block statically over both edges, L4 hash.
  - `floating`: the block to `upstream.floating_next_hop`, which the active
    edge has to hold - nothing in the lab holds it yet.
- **Internet client**: the `inet` namespace on upstream-sim, on the far side of
  the provider: `ssh ubuntu@192.168.122.30 sudo ip netns exec inet ...`.
- **NAT**: upstream-sim masquerades towards the real LAN, but never the public
  block, so those addresses stay the lab's public ones.
- **Edges**: each edge answers public DNS on `203.0.113.53` and serves the
  public addresses reserved from the block (`GOA_PUBLIC_CIDR`), such as a load
  balancer's, announcing them over BGP in `bgp` mode (`GOA_BGP_*`). The rest of
  the block is answered with ICMP unreachable rather than sent out their
  default route to the real LAN.
- **Lab machines**: every VM resolves the public zones (`public_dns_domains`)
  through the public DNS and nothing else, and reaches the block through
  upstream-sim.

`create-vms.yml` defines `wan-sim`, creates upstream-sim and plugs each VM
listed with a `wan_ip` into it, first pinning every VM's primary interface to
its MAC: cloud-init matches it by name (`e*`), which would otherwise hand the
new NIC the same static address. `site.yml` addresses the transit legs and sets
up upstream-sim. Then:

```bash
ansible-playbook verify-upstream.yml
```

checks, from the `inet` client: its gateway and both edges are reachable; in
`bgp` mode, every edge's session is up; the block - in `bgp` mode, the public
DNS address every edge announces - is routed to every edge, 64 TCP flows
spread over both; a packet for an address nothing serves is answered as
unreachable; the public DNS refuses recursion; the NAT exempts
the block while `inet` still reaches the LAN; and, once the Terraform demo is
applied, `https://www.demo.test/` answers with a certificate `inet` verifies.

Every lab machine trusts the platform's public root (`public_trust` role): the
playbook reads it from the API, which creates it at its first start, and
installs it with `update-ca-certificates`, as an Internet host trusts a public
CA.

To reach the block from your workstation, route it through upstream-sim:
`sudo ip route add 203.0.113.0/24 via 192.168.122.30`.

## Access

- Dashboard: `http://192.168.122.10:8088`
- Grafana:   `http://192.168.122.10:3000` (admin / infra)
- API:       `http://192.168.122.10:8080` (needs a Bearer JWT from the IdP)

## Tear down

```bash
ansible-playbook --ask-become-pass destroy-vms.yml
```

## Topology

Hosts and addresses are defined in `group_vars/all.yml` (`vms`) and mirrored in
`inventory.ini`. The default is one control host (`192.168.122.10`, 3 GiB),
two agents (`192.168.122.21`, `.22`, 1.5 GiB each) and upstream-sim
(`192.168.122.30`, 768 MiB) on the libvirt `default` NAT network, about 7 GiB of
RAM on the libvirt host in total.
