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
| DNS zone/record  | a per-VPC dnsmasq serving the zones' A/AAAA records |
| Disk             | a backing image, optionally dm-crypt (LUKS) encrypted |
| Security group   | an allow-list iptables chain |
| WAF policy       | an iptables chain attached inbound to the target |
| Load balancer    | an IPVS virtual service on a VIP, full-NAT through the node port |
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

Each host carries the subnet gateways and load balancer addresses on its own
bridge. The bridge takes the same MAC on every host, derived from the VPC, and
a `tc` filter keeps frames from that MAC off the overlay: the gateway is an
anycast gateway, and an instance always routes through the host it runs on.

What the host itself sends into the VPC must not carry that MAC, or the reply
would stay on the receiving host. It leaves through the VPC's node port:

```
np-<uid> <-> nb-<uid>  veth pair, nb-<uid> enslaved to br-<uid>
  np-<uid>  a MAC of its own; this host's address in each subnet; the subnets'
            connected routes (the gateways sit on the bridge as noprefixroute)
  address   counted down from the subnet's last usable host by the host's rank
            among the registered nodes: .254, .253, ... in a /24
  ARP       arp_ignore=1 on the bridge and the node port
```

At most five hosts get an address per subnet, and the compute allocator never
hands those top addresses out.

## Load balancers

A load balancer is an IPVS virtual service on a VIP held by every host's
bridge, so an instance reaches it through its own host. IPVS forwards in NAT
mode, and the connections it forwards leave through the node port masqueraded
to this host's address there (`net.ipv4.vs.conntrack=1` exposes them to
netfilter). Every backend therefore replies to the host that took the
connection: backends on other hosts and clients in a backend's own subnet are
both served.

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
free host in the subnet), the security-group chain and each disk's device path,
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
