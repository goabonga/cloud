# Realization

The control plane records desired state; **infra-agent** turns it into kernel
state. This page describes how the agent reconciles resources on a host.

## The reconcile loop

The agent runs a set of *passes* on a fixed interval. Each pass implements:

```go
type ReconcilePass interface {
    Name() string
    ReconcileAll(ctx context.Context) error
}
```

On every tick the agent runs each pass, which lists its resources and reconciles
them one by one. Reconciliation is **level-triggered and idempotent**: a pass
computes the desired kernel state from the current `spec` and converges to it,
so a missed tick or a restart is harmless.

A single reconcile is:

```
load resource
  deleting?  -> run finalizer, drop the finalizer, delete the record
  otherwise  -> attach the finalizer, realize the spec, record status
```

Cross-resource dependencies are handled by **requeue**: if a parent is not ready
(a VPC bridge is missing, a subnet has no gateway, a disk is not provisioned), the
resource is left `Pending` and retried on the next tick rather than failing.

## Backends behind interfaces

Every kernel mutation sits behind an interface (`NetworkBackend`,
`FirewallBackend`, `SecurityGroupBackend`, `DiskBackend`, `ComputeBackend`). The
real implementations shell out to iproute2, iptables, cryptsetup, mount and
go-containerregistry and require root / `CAP_NET_ADMIN`. Fakes stand in for unit
tests, so the reconcile logic is exercised without a privileged host. The
privileged paths are covered by `//go:build integration` tests that skip when not
run as root.

## What each pass realizes

| Resource         | Kernel state |
| ---------------- | ------------ |
| VPC              | a Linux bridge (`br-<uid>`), joined across hosts by a VXLAN device (`vx-<uid>`) |
| Subnet           | the gateway address on the VPC bridge |
| Internet gateway | IPv4 forwarding + a MASQUERADE rule for the VPC CIDR |
| Peering          | a veth pair joining the two VPC bridges |
| DNS zone/record  | answered by the agent: a resolver per VPC, and public zones on a public address |
| Disk             | a backing image, optionally dm-crypt (LUKS) encrypted |
| Disk file        | the file, written into the disk through the mounts of this host's instances |
| SSL CA           | the CA certificate, in the trust bundle of the instances that trust it |
| Security group   | an allow-list iptables chain |
| WAF policy       | an iptables chain attached inbound to the target |
| Load balancer    | an IPVS virtual service in the VPC's load-balancer namespace, full-NAT through its node port; on the edges, also on its public address |
| Public IP address | an address of the edges' public block, reserved in the shared store |
| Compute          | a network namespace running an OCI image |

## VPCs across hosts

Every agent builds each VPC on a bridge of its own. When the agent has a node
identity (`GOA_NODE_ID`) and that node is registered, the overlay pass joins
those bridges into one L2 segment:

```
vx-<uid>  VXLAN device enslaved to br-<uid>, UDP 4789
  VNI         derived from the VPC UID, identical on every host
  local       this node's registered address
  flood list  every other node's address (head-end replication); remote MACs
              are learned from traffic
  MTU         1450, and 1450 on every compute veth, leaving room for the 50
              bytes of encapsulation on a 1500-byte underlay
```

Nodes joining or leaving update each device's flood list on the next tick, and
the devices of deleted VPCs are removed.

Each host carries the subnet gateways on its own bridge. The bridge takes the
same MAC on every host, derived from the VPC, and a `tc` filter keeps frames
from that MAC off the overlay: the gateway is an anycast gateway, and an
instance always routes through the host it runs on. The host answers only its
own instances - their gateway, their resolver - so the subnets' connected
routes sit on the bridge.

## Load-balancer namespaces

Every VPC has, on every host, a network namespace of its own for its load
balancers: their virtual services live there rather than in the host, so two
VPCs never share a virtual-service table. The namespace is held by a systemd
unit, `infra-netns@lb-<uid>.service` (`PrivateNetwork=yes`, a sleeping
process with no capability): the agent runs in a private mount namespace,
where a namespace it named under `/run/netns` would vanish when it restarts,
so the unit keeps it alive and the agent enters it through
`/proc/<MainPID>/ns/net`. Restarting the agent leaves the load balancers
serving; if the holder dies, systemd starts it again and the agent plugs the
new, empty namespace in on its next pass.

```
la0 <-> la-<uid>  anycast port, la-<uid> enslaved to br-<uid>
  la0       the VIPs; a MAC derived from the VPC, the same on every host, kept
            off the overlay by a second tc filter: an instance's ARP for a VIP
            is answered by its own host's namespace
np0 <-> nb-<uid>  node port, nb-<uid> enslaved to br-<uid>
  np0       a MAC of its own; this host's address in each subnet, counted down
            from the subnet's last usable host by the host's rank among the
            registered nodes: .254, .253, ... in a /24
pub0 <-> lp-<uid> public leg, on an edge only: a routed veth to the host
  pub0      169.254.0.2, default route via the host's 169.254.0.1; the public
            addresses on lo
ARP         arp_ignore=1 on the bridge, la0 and np0; rp_filter off in the
            namespace, where a request comes in on la0 and its reply leaves np0
```

At most five hosts get an address per subnet, and the compute allocator never
hands those top addresses out.

## Load balancers

A load balancer is an IPVS virtual service in its VPC's load-balancer
namespace, on a VIP held by the anycast port of every host's namespace, so an
instance reaches it through its own host. An instance in the VIP's subnet
resolves it on the bridge directly; one outside sends it to its gateway, and
the host routes the VIP onto the bridge (`send_redirects=0` there, since it
forwards back out the interface the request came in on). IPVS forwards in NAT
mode, and the connections it forwards leave through the node port masqueraded
to this host's address there (`net.ipv4.vs.conntrack=1` exposes them to
netfilter). Every backend therefore replies to the namespace that took the
connection: backends on other hosts and clients in a backend's own subnet are
both served.

An agent that realized load balancers in the host itself, before the
namespace, left their VIP on the bridge, their public address on the loopback,
their virtual services and the node port in the host. The first pass of a
newer agent removes them.

## Public addresses

An agent with `GOA_PUBLIC_CIDR` is an edge: the public block is routed to it.
It resolves every `ip_address` of type `public` to an address of that block -
the one asked for, or else the first free one, never one another `ip_address`
asks for by name nor the public DNS address - reserved in the shared store
under `ipam/public/<ip>`, so edges reconciling at once settle on the same
address. A conflict puts the latecomer in `Error`. `ip_address` carries no
finalizer, so the reservations of deleted ones are released on the next pass.

A load balancer naming a public `ip_address` (`publicIpId`) is also served on
the edges on that address: the edge routes it to its VPC's load-balancer
namespace through the public leg, where it sits on the loopback with the same
IPVS virtual service as the VIP, full-NAT included, so traffic the upstream
routes to an edge for it reaches the backends on any host. The namespace's
replies leave through its default route, back through the leg. Each edge tracks which public
address it serves per load balancer and removes it when the load balancer
moves to another one or is deleted.

## BGP on the edges

An edge with `GOA_BGP_ASN` announces the public addresses it serves to its
upstream routers over BGP, from a GoBGP speaker embedded in the agent:

```
GOA_BGP_ASN        the edge's AS, the same on every edge
GOA_BGP_ROUTER_ID  an IPv4 address of the edge, e.g. its transit address
GOA_BGP_PEERS      upstream routers, "addr@asn[,addr@asn...]"
GOA_BGP_BFD        "true" (default) runs BFD on UDP 3784, 300 ms x 3
```

Each address is a /32 with the edge as next hop: the public DNS address while
the DNS pass serves it, and each load balancer's public address while the
load-balancer pass serves it without error. The BGP pass runs last in the
loop, so it announces what that very pass served, and withdraws an address the
moment it stops being served. A store that cannot be read leaves the last
announcements in place, as the data plane keeps serving. The speaker opens the
sessions itself and never listens; it redials within a second after a reset.

The upstream thus spreads an address's traffic over the edges announcing it,
and an edge's routes leave with the edge: within a second of its going silent
(BFD), within the 3 s hold time without BFD, and at once when the agent stops,
since it closes its sessions on the way out - which also keeps the upstream
from sending DNS queries to an edge whose listener restarts with the agent.

## DNS

The agent serves DNS itself; no resolver process runs on the host.

- **VPC resolver.** Each VPC's first address - the nameserver its instances
  are handed - is assigned as a /32 on the VPC bridge on every host, like the
  subnet gateways, so an instance always queries its own host. It answers the
  private zones attached to the VPC and every public zone authoritatively, and
  forwards any other name to the host's own resolvers.
- **Public DNS.** With `GOA_DNS_PUBLIC_ADDR` set (on the edges, an address of
  the public block), the agent assigns it as a /32 on the loopback and answers
  every public zone there, authoritatively and nothing else: private zones and
  other names are refused, so it is never an open resolver. Every edge holding
  the address answers, and the upstream's ECMP spreads queries over them.

Both serve UDP and TCP from a view rebuilt from the store every tick and
swapped in atomically. Names in a zone get authoritative answers: the records,
CNAMEs followed within the zone, or NXDOMAIN / NODATA with the zone's SOA. A
record whose value does not parse is left out and put in `Error`.

## Encrypted disks

A disk with a `kmsKeyId` is encrypted at rest. The agent derives a per-disk LUKS
passphrase from its master key with HKDF-SHA256:

```
passphrase = HKDF(master, info = "disk:" + kmsKeyId + ":" + diskUID)
```

The master key is supplied to the agent as base64 in `GOA_KMS_KEY`; without it,
a disk that requests encryption is held in `Error` rather than written in the
clear. The backend runs `cryptsetup luksFormat`/`open` and lays an ext4
filesystem on `/dev/mapper/infra-<uid>`.

## Disk files

A disk file puts content at a path of a disk. The agent writes it through the
mount of every instance on its host that attaches the disk read-write, so the
instance sees it at `<mount path>/<path>`; a host with no such instance leaves
it alone. The instance controls its disk, so the path is cleaned against the mount and
walked one component at a time with `O_NOFOLLOW`, never following a symlink,
and missing
directories are created 0755. The mode defaults to `0644` and is set
explicitly, whatever the agent's umask. The file is rewritten only when its
content differs. Deleting a disk file leaves its content on the disk.

A disk file can hold a part of an `ssl_cert` instead of literal content:
`certificate`, `chain` (the certificate followed by its CA's, as nginx's
`ssl_certificate` expects) or `private_key`. The agent renders it from the
store, decrypting the key with the KMS key (`GOA_KMS_KEY`), so the key reaches
the instance's disk without passing through Terraform; a private key defaults
to mode `0600`. A certificate not issued yet leaves the file `Pending`, and an
agent without the KMS key puts it in `Error`.

## CA trust

Every instance on a host trusts the platform's global CAs - the public root,
`public-root`, which the API creates at its first start - and the CAs whose
`vpcIds` name its VPC. The agent keeps their certificates in a block between
`# BEGIN infra-agent trusted CAs` and `# END infra-agent trusted CAs` in the
system trust bundle of the instance's rootfs: `/etc/ssl/certs/ca-certificates.crt`
(Debian, Ubuntu, Alpine), `/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem`
(Fedora, RHEL) and `/etc/ssl/ca-bundle.pem` (SUSE), whichever exist; with none,
it creates the first. The image's own CAs stay untouched. Bundles are opened
like disk files, beneath the rootfs and never through a symlink, and rewritten
every pass when the CAs change: a CA created, rescoped or deleted reaches running
instances on the next tick.

## Compute

A compute instance is realized as a namespaced container, not a VM:

```
veth pair: vh-<hash> (host) <-> vp-<hash> (namespace)
  vh-<hash> enslaved to the VPC bridge
  vp-<hash> carries the allocated address + default route via the subnet gateway
cgroup v2: /sys/fs/cgroup/infra/<uid>  (cpu.max, memory.max, pids.max)
rootfs:    OCI image pulled and flattened, run under pivot_root
firewall:  FORWARD/OUTPUT -d <ip> -j <security-group chain>; DNAT for port maps
disks:     attached devices mounted into the rootfs
```

The reconciler resolves the subnet gateway, the VPC bridge, an address (the first
free host in the subnet, reserved in the shared store under `ipam/<subnet>/<ip>`
with a create-if-absent compare-and-swap, so agents allocating at once on
different hosts never take the same one; a reservation is released when the
instance is deleted), the security-group chain and each disk's device path,
then asks the backend to bring the namespace up. Creation happens once; later
ticks are a no-op while the namespace exists. The finalizer kills the cgroup,
removes the veth, deletes the namespace and the firewall rules, and unmounts the
disks.

## The end-to-end chain

A declared topology converges in dependency order across ticks:

```
VPC (bridge, VXLAN to the other hosts)
  -> subnet (gateway on the bridge)
       -> internet gateway (NAT for egress)
       -> security group (allow-list chain)  +  disk (encrypted)
            -> compute (namespace on the bridge, address from the subnet,
                        chain attached, encrypted disk mounted)
```

Until each dependency is `Ready` the compute stays `Pending`; once they are, the
agent assigns the address, attaches the chain, mounts the disk and launches the
image entrypoint.
