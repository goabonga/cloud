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
| VPC              | a Linux bridge (`br-<uid>`) in a VRF of its own (`vrf-<uid>`), joined across hosts by a VXLAN device (`vx-<uid>`) |
| Subnet           | the gateway address on the VPC bridge |
| Internet gateway | IPv4 forwarding, the host's default route in the VPC's table and a MASQUERADE rule for the VPC CIDR; with its egress proxy, the filtering below |
| Peering          | a veth pair joining the two VPC bridges, and each VPC's CIDR routed from the other's table |
| DNS zone/record  | answered by the agent: a resolver per VPC, and public zones on a public address |
| Disk             | a backing image, optionally dm-crypt (LUKS) encrypted |
| Disk file        | the file, written into the disk through the mounts of this host's instances |
| SSL CA           | the CA certificate, in the trust bundle of the instances that trust it |
| Security group   | an allow-list iptables chain, jumped to on the VPC's bridge |
| WAF policy       | an iptables chain attached inbound to the target |
| Load balancer    | an IPVS virtual service in the VPC's load-balancer namespace, full-NAT through its node port; on the edges, also on its public address |
| Listener         | infra-lb in the VPC's load-balancer namespace, on the load balancer's VIP and, on the edges, public address |
| Public IP address | an address of the edges' public block, reserved in the shared store |
| Compute          | a network namespace running an OCI image |
| MicroVM          | an infra-hypervisor process attached to the VPC bridge by a TAP device |

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

## VRF per VPC

Every VPC routes in a VRF of its own. Its bridge is enslaved to `vrf-<uid>`,
whose routing table - `0x01000000` plus the VPC's VNI, the same on every host -
holds the subnets' connected routes, the resolver address, the VIPs and, with
an internet gateway, the host's default route. The table ends with an
unreachable default, so a lookup that finds nothing there never falls through
to the main table. Two VPCs therefore never see each other's routes, their
CIDRs may overlap, and the host's main table reaches into no VPC: from a host,
`ip vrf exec vrf-<uid> ...` runs a command inside one.

What crosses between a VPC and the main table is steered by marks, at policy
priority 900, ahead of the kernel's l3mdev rule:

```
mangle PREROUTING
  -i br-<uid>, NEW, no mark   CONNMARK <table>   an instance opens a connection
  connmark <table>, REPLY     MARK <table>       its replies: the VPC's table
  connmark <table>|0x40000000, REPLY
                              MARK <same>        replies to a port map: main
rules  fwmark <table> lookup <table>; fwmark <table>|0x40000000 lookup main
```

A packet from the VPC goes through PREROUTING twice, on the bridge then on
the VRF device, and what the host sends goes through OUTPUT first on the VRF
device, untracked, then on the bridge. So packet marks are only set on replies,
told apart by the connection's direction - a packet for the host's own
listeners must reach them through the VRF - and every iptables rule naming an
instance matches it on its VPC's bridge (`-o br-<uid>`) as well as its address,
which two VPCs may share.

The `vrf` module ships in the kernel's extra modules
(`linux-modules-extra-<kernel>` on Ubuntu), which cloud images leave out.

Connection tracking has no zone per VPC: two connections whose original
5-tuples are identical - same instance address, same source port, same
destination - in two VPCs on one host at once would be taken for one.

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
the host routes the VIP onto the bridge in the VPC's table (`send_redirects=0`
there, since it forwards back out the interface the request came in on). IPVS forwards in NAT
mode, and the connections it forwards leave through the node port masqueraded
to this host's address there (`net.ipv4.vs.conntrack=1` exposes them to
netfilter). Every backend therefore replies to the namespace that took the
connection: backends on other hosts and clients in a backend's own subnet are
both served.

An agent that realized load balancers in the host itself, before the
namespace, left their VIP on the bridge, their public address on the loopback,
their virtual services and the node port in the host. The first pass of a
newer agent removes them.

## Listeners

A load balancer's listeners are served by infra-lb, running as
`infra-lb@lb-<uid>.service` in the VPC's load-balancer namespace on every host:
the unit joins the namespace `infra-netns@lb-<uid>` holds
(`JoinsNamespaceOf=`, `BindsTo=`), with no capability but binding ports below
1024. The listener pass builds its configuration for each VPC:

- each listener on its load balancer's VIP and, on an edge, its public
  address, both already on the namespace's anycast port and loopback;
- each certificate as its chain and private key, rendered from the store and
  decrypted with the KMS key, and each target group's backend CA;
- each target group's targets that have an address, with their port.

It writes it to `/run/infra-lb/lb-<uid>/config.json` (0600, it holds private
keys), starts the unit, or has it reload (SIGHUP) when the configuration
changed, and stops it with the VPC's last listener. A listener waits
(`Pending`) while its load balancer has no address or a certificate is not
issued, and fails if it takes the load balancer's own layer-4 port. infra-lb
reports back through `status.json`: a listener is `Ready` once it holds its
port on every address, and each target group's status lists its targets'
health as each host's infra-lb checks it, every host replacing its own
entries. infra-lb keeps serving its last configuration when the agent or the
store is down.

## Egress proxy

An internet gateway with its egress proxy enabled filters its VPC's egress.
On every host:

```
infra-egress@lb-<uid>   infra-lb serving egress listeners (see the data plane
                        page), in the VPC's load-balancer namespace, on this
                        host's node-port address in the VPC's first subnet:
                        18080 egress-http, 18443 egress-tls, 13128 egress-proxy;
                        resolving through the VPC's resolver, going out through
                        the namespace's host leg, masqueraded to the host
proxy address           the VPC's second address, on the anycast port; infra-lb@
                        balances its ports 80, 443 and 3128 over the proxies of
                        every host, health-checked over TCP every 2 s
INFRA-EGR-<hash> (nat)  jumped to from PREROUTING on the bridge: HTTP and HTTPS
                        to anything but private blocks and allowed addresses
                        DNAT to the proxy address
INFRA-EGF-<hash>        jumped to first in FORWARD for new connections out of
                        the uplink carrying the VPC's mark (see the VRF section):
                        allowed addresses accepted, anything else rejected
```

An instance reaches the Internet only through the proxy, for the allowed
domains, transparently or explicitly on the proxy address's port 3128, and
directly only to the allowed addresses. One host's proxy failing, the
instances of that host go out through the others': the balancing is the
health-checked pool, not the local proxy. The proxies' own traffic enters the
host from the namespace's leg, without the VPC's mark, and is not filtered
again. The chains are found by name, so the agent also removes those of a
gateway deleted or disabled while it was down; the gateway's status names
the proxy address.

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
  subnet gateways, so an instance always queries its own host. Its listener is
  bound to the VPC's VRF device (`SO_BINDTODEVICE`), where the address lives,
  so two VPCs may use the same one. It answers the private zones attached to
  the VPC and every public zone authoritatively, and forwards any other name to
  the host's own resolvers.
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
firewall:  FORWARD/OUTPUT -o <bridge> -d <ip> -j <security-group chain>;
           DNAT for port maps, marked into the VPC's table and back
disks:     attached devices mounted into the rootfs
```

The reconciler resolves the subnet gateway, the VPC bridge, an address (the first
free host in the subnet, reserved in the shared store under `ipam/<subnet>/<ip>`
with a create-if-absent compare-and-swap, so agents allocating at once on
different hosts never take the same one; a reservation is released when the
instance is deleted), the security-group chain and each disk's device path,
then asks the backend to bring the namespace up. The agent records the applied
request and host launcher PID/start time in its private runtime state. Later
ticks keep the firewall rules in place only when that configuration matches
and the launcher is still running. Configuration drift, a dead process or
missing runtime state triggers teardown and recreation. Startup errors are
returned to the reconciler instead of reporting a ready instance. Process
liveness is checked at reconciliation intervals; this is not an application
health probe. The finalizer kills the cgroup,
removes the veth, deletes the namespace and the firewall rules, and unmounts the
disks.

## MicroVMs

A micro-VM is a second realization path alongside compute, for workloads that
need a kernel of their own rather than a namespaced container. It is a real
VM booted under **infra-hypervisor**, a hand-rolled, pure-Go VMM driving
`/dev/kvm` directly (no cgo, no external VMM binary — see
[go-hypervisor.md](go-hypervisor.md) for the design), which the agent drives
like any other external tool (`iproute2`, `iptables`): a process the agent
starts and talks to over its own control socket, not a Go dependency itself
(`internal/hypervisor`'s packages are, but the agent only ever spawns and
speaks to the compiled `infra-hypervisor` binary, shipped alongside
`infra-agent` in the same package).

```
TAP device: tap-<hash>, enslaved to the VPC bridge, created with
            `ip tuntap add ... mode tap` (the same device-creation style as
            the rest of this package, not a raw netlink/ioctl call) - then
            re-opened by infra-hypervisor itself (TUNSETIFF) for frame I/O,
            since unlike cloud-hypervisor before it, it needs the fd
            directly rather than attaching by name
infra-hypervisor: one process per instance, its control protocol
            (newline-delimited JSON, not REST) on a unix socket under the
            agent's state directory; a single `create` request configures
            and boots it
kernel cmdline: the allocated address is passed as a static `ip=` directive,
            so the guest configures eth0 at boot - `net.ifnames=0
            biosdevname=0` keep it named eth0, since udev's predictable
            naming otherwise races the kernel's own "ip=" processing and
            often wins - and a `ds=nocloud-net` directive points cloud-init
            at the agent's seed server
cloud-init: one HTTP listener for every micro-VM on the host (port 8912,
            every interface, so each VPC's instances reach it at their own
            subnet gateway) answers the NoCloud datasource:
            `<uid>/meta-data` and `<uid>/user-data` - `userData` verbatim
            when set, otherwise a minimal cloud-config from `hostname` and
            `sshAuthorizedKey` - keyed by instance UID, forgotten on delete
boot disk:  `image` (a URL or a local path) is fetched/copied once into a
            node-local cache keyed by its hash, then cloned - copy-on-write
            (FICLONE) where the filesystem supports it, a hole-preserving
            (SEEK_DATA/SEEK_HOLE) copy otherwise - into the instance's own
            disk, so instances never share writable state, the cache is
            never mutated, and a mostly-empty source stays mostly-empty on
            disk either way
firewall:   FORWARD/OUTPUT -d <ip> -j <security-group chain>, the same rule
            shape as compute
```

The reconciler resolves the subnet gateway, the VPC bridge, an address
(reserved the same way as compute's) and the security-group chain, then asks
the backend to boot infra-hypervisor. Creation happens once: a subsequent
pass that finds the process still alive is a no-op. The finalizer asks it to
shut down over the control socket (killing it if it doesn't respond within a
second), removes the TAP device and the firewall rules.

The kernel and initramfs are host-prepared absolute paths given in the spec
(`kernelPath`, `initrdPath`) - there is no kernel fetch/cache. There is no
CLI support (the CLI itself only covers `vpc` today, nothing resource-generic
yet); the control-plane API, the agent and the Terraform provider
(`infra_microvm`) realize and expose this resource. Placement is scheduled
by a separate `microvm-scheduler` controller, with a caveat on capacity
accounting shared with compute - see
[scheduling](scheduling.md#microvms-and-compute-share-nodes-not-capacity-accounting).

## The end-to-end chain

A declared topology converges in dependency order across ticks:

```
VPC (bridge in its VRF, VXLAN to the other hosts)
  -> subnet (gateway on the bridge)
       -> internet gateway (NAT for egress)
       -> security group (allow-list chain)  +  disk (encrypted)
            -> compute (namespace on the bridge, address from the subnet,
                        chain attached, encrypted disk mounted)
```

Until each dependency is `Ready` the compute stays `Pending`; once they are, the
agent assigns the address, attaches the chain, mounts the disk and launches the
image entrypoint.
