// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/lbproxy"
)

// An internet gateway with its egress proxy enabled filters its VPC's
// egress. On every host:
//
//   - an egress proxy, infra-egress@<namespace> - infra-lb serving egress
//     listeners - runs in the VPC's load-balancer namespace, on this host's
//     node-port address, and goes out through the namespace's host leg,
//     masqueraded to the host's address;
//   - the proxy address, the VPC's second address, sits on the namespace's
//     anycast port, where infra-lb balances its ports 80, 443 and 3128 over
//     the proxies of every host, health-checked: one host's proxy failing,
//     its instances go out through the others';
//   - the instances' HTTP and HTTPS to public addresses are redirected there
//     (nat chain INFRA-EGR-<hash>), and any other new connection out of the
//     uplink is refused unless its destination is allowed (filter chain
//     INFRA-EGF-<hash>, matched on the VPC's connection mark, see vrf.go).
//
// The proxies' own traffic enters the host from the namespace's leg, with no
// VPC mark, and is not filtered again.

// The ports of the egress proxy: on the proxy address, as instances reach it,
// and on each host's node-port address, as infra-lb reaches the proxies.
const (
	egressPortHTTP        = 80
	egressPortTLS         = 443
	egressPortExplicit    = 3128
	egressBackendHTTP     = 18080
	egressBackendTLS      = 18443
	egressBackendExplicit = 13128
)

// privateBlocks are left out of the redirection: traffic to them is the
// VPC's own, a peer's or the LAN's, not the Internet's, and the filter decides
// whether it may leave.
var privateBlocks = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16", "127.0.0.0/8"}

// EgressSetup is what a VPC's egress filtering needs on the host.
type EgressSetup struct {
	VPCID  string
	Bridge string
	// VIP is the proxy address.
	VIP string
	// Uplink is the internet gateway's host interface.
	Uplink  string
	Allowed []resource.EgressAddress
}

// EgressBackend puts a VPC's egress filtering in place on the host.
type EgressBackend interface {
	// EnsureEgress puts the proxy address on the namespace, plugs the
	// namespace's host leg in, and syncs the redirection and the filter.
	// Idempotent.
	EnsureEgress(ctx context.Context, e EgressSetup) error
	// Prune removes the redirection and filter of every VPC but keep.
	Prune(ctx context.Context, keep []string) error
}

// egressChains names a VPC's redirection and filter chains.
func egressChains(vpcID string) (nat, filter string) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(vpcID))
	s := fmt.Sprintf("%08x", h.Sum32())
	return "INFRA-EGR-" + s, "INFRA-EGF-" + s
}

// secondHostOf returns the second usable address of cidr, e.g. 10.0.0.2 in
// 10.0.0.0/16: the first is the VPC's resolver.
func secondHostOf(cidr string) string {
	ip := net.ParseIP(firstHostOf(cidr)).To4()
	if ip == nil {
		return ""
	}
	next := make(net.IP, 4)
	copy(next, ip)
	next[3]++
	return next.String()
}

// EgressReconciler is the egress pass. It runs before the listener pass,
// which serves the proxy address with what ExtraConfig returns.
type EgressReconciler struct {
	igws     *IGWRegistry
	vpcs     *VPCRegistry
	subnets  *SubnetRegistry
	nodes    *NodeRegistry
	backend  EgressBackend
	plane    DataPlane
	nodeName string

	extra map[string]*lbproxy.Config
}

// NewEgressReconciler returns the egress pass for the node named nodeName.
func NewEgressReconciler(igws *IGWRegistry, vpcs *VPCRegistry, subnets *SubnetRegistry, nodes *NodeRegistry,
	backend EgressBackend, plane DataPlane, nodeName string) *EgressReconciler {
	return &EgressReconciler{igws: igws, vpcs: vpcs, subnets: subnets, nodes: nodes, backend: backend, plane: plane,
		nodeName: nodeName, extra: map[string]*lbproxy.Config{}}
}

// Name identifies the reconcile pass.
func (r *EgressReconciler) Name() string { return "egress" }

// ExtraConfig implements ListenerSource: the listeners and target groups
// serving the VPC's proxy address, as of this pass.
func (r *EgressReconciler) ExtraConfig(vpcID string) ([]lbproxy.Listener, []lbproxy.TargetGroup) {
	cfg, ok := r.extra[vpcID]
	if !ok {
		return nil, nil
	}
	return cfg.Listeners, cfg.TargetGroups
}

// ReconcileAll puts each enabled egress proxy in place and removes the
// others'.
func (r *EgressReconciler) ReconcileAll(ctx context.Context) error {
	igws, err := r.igws.List()
	if err != nil {
		return fmt.Errorf("manager: list igws: %w", err)
	}
	subnets, err := r.subnets.List()
	if err != nil {
		return fmt.Errorf("manager: list subnets: %w", err)
	}
	nodes, err := r.nodes.List()
	if err != nil {
		return fmt.Errorf("manager: list nodes: %w", err)
	}
	ranks := nodeRanks(nodes)

	var errs []error
	keep := []string{}
	served := map[string]bool{}
	extra := map[string]*lbproxy.Config{}
	for i := range igws {
		igw := &igws[i]
		p := igw.Spec.EgressProxy
		if igw.Metadata.IsDeleting() || p == nil || !p.Enabled {
			r.setStatus(igw, nil)
			continue
		}
		vpc, err := r.vpcs.Get(igw.Spec.VPCID)
		if err != nil || vpc.Status.BridgeName == "" || igw.Status.HostIface == "" {
			// The VPC or the gateway itself is not up yet.
			continue
		}
		vip := secondHostOf(vpc.Spec.CIDR)
		subnet := firstSubnetOf(subnets, vpc.Metadata.UID)
		if vip == "" || subnet == "" {
			continue
		}
		keep = append(keep, vpc.Metadata.UID)
		if err := r.backend.EnsureEgress(ctx, EgressSetup{
			VPCID: vpc.Metadata.UID, Bridge: vpc.Status.BridgeName, VIP: vip,
			Uplink: igw.Status.HostIface, Allowed: p.AllowedAddresses,
		}); err != nil {
			errs = append(errs, fmt.Errorf("igw %s egress: %w", igw.Metadata.UID, err))
			continue
		}
		extra[vpc.Metadata.UID] = balancing(vpc.Metadata.UID, vip, subnet, ranks)
		if rank, ok := ranks[r.nodeName]; ok {
			if local, err := nodeAddress(subnet, rank); err == nil {
				ns := lbNamespaceName(vpc.Metadata.UID)
				if err := r.plane.Apply(ctx, ns, proxies(local, firstHostOf(vpc.Spec.CIDR), p)); err != nil {
					errs = append(errs, fmt.Errorf("igw %s egress proxy: %w", igw.Metadata.UID, err))
				} else {
					served[ns] = true
				}
			}
		}
		r.setStatus(igw, &resource.EgressProxyStatus{Address: vip})
	}
	r.extra = extra
	if err := r.backend.Prune(ctx, keep); err != nil {
		errs = append(errs, err)
	}
	for _, ns := range r.plane.Serving() {
		if !served[ns] {
			if err := r.plane.Stop(ctx, ns); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// setStatus records the gateway's egress proxy status, writing only on change.
func (r *EgressReconciler) setStatus(igw *resource.IGW, st *resource.EgressProxyStatus) {
	cur := igw.Status.EgressProxy
	if (cur == nil && st == nil) || (cur != nil && st != nil && *cur == *st) {
		return
	}
	igw.Status.EgressProxy = st
	_ = r.igws.Put(igw)
}

// nodeRanks numbers the registered nodes by UID, as the node-port addresses
// do (see SubnetReconciler).
func nodeRanks(nodes []resource.Node) map[string]int {
	uids := make([]string, 0, len(nodes))
	for i := range nodes {
		uids = append(uids, nodes[i].Metadata.UID)
	}
	slices.Sort(uids)
	ranks := make(map[string]int, len(uids))
	for i, u := range uids {
		ranks[u] = i
	}
	return ranks
}

// firstSubnetOf returns the CIDR of the VPC's first live subnet, by UID: the
// node ports' addresses in it are where the proxies listen.
func firstSubnetOf(subnets []resource.Subnet, vpcID string) string {
	best, cidr := "", ""
	for i := range subnets {
		s := &subnets[i]
		if s.Metadata.IsDeleting() || s.Spec.VPCID != vpcID {
			continue
		}
		if best == "" || s.Metadata.UID < best {
			best, cidr = s.Metadata.UID, s.Spec.CIDR
		}
	}
	return cidr
}

// balancing is what infra-lb serves on the proxy address: each port spliced
// to the proxies of every host, health-checked over TCP.
func balancing(vpcID, vip, subnet string, ranks map[string]int) *lbproxy.Config {
	cfg := &lbproxy.Config{}
	for _, p := range []struct {
		name          string
		front, behind int
	}{
		{"http", egressPortHTTP, egressBackendHTTP},
		{"tls", egressPortTLS, egressBackendTLS},
		{"proxy", egressPortExplicit, egressBackendExplicit},
	} {
		group := "egress-" + p.name + "@" + vpcID
		g := lbproxy.TargetGroup{
			Name: group, Protocol: lbproxy.ProtocolTCP,
			HealthCheck: lbproxy.HealthCheck{Protocol: lbproxy.ProtocolTCP, IntervalSeconds: 2, TimeoutSeconds: 1, HealthyThreshold: 2, UnhealthyThreshold: 2},
			Targets:     []lbproxy.Target{},
		}
		for node, rank := range ranks {
			addr, err := nodeAddress(subnet, rank)
			if err != nil {
				continue
			}
			g.Targets = append(g.Targets, lbproxy.Target{ID: node, Address: addr, Port: p.behind, Weight: 1})
		}
		slices.SortFunc(g.Targets, func(a, b lbproxy.Target) int { return strings.Compare(a.ID, b.ID) })
		cfg.TargetGroups = append(cfg.TargetGroups, g)
		cfg.Listeners = append(cfg.Listeners, lbproxy.Listener{
			Name: group, Address: vip, Port: p.front, Protocol: lbproxy.ProtocolTCP, DefaultTargetGroup: group,
		})
	}
	return cfg
}

// proxies is this host's egress proxy configuration: its three listeners on
// its node-port address, resolving through the VPC's resolver.
func proxies(local, resolver string, p *resource.EgressProxySpec) *lbproxy.Config {
	pol := &lbproxy.EgressPolicy{AllowedDomains: p.AllowedDomains, Resolver: net.JoinHostPort(resolver, "53")}
	for _, a := range p.AllowedAddresses {
		pol.AllowedCIDRs = append(pol.AllowedCIDRs, a.CIDR)
	}
	listener := func(protocol string, port int) lbproxy.Listener {
		return lbproxy.Listener{Name: protocol, Address: local, Port: port, Protocol: protocol, Egress: pol}
	}
	return &lbproxy.Config{
		Listeners: []lbproxy.Listener{
			listener(lbproxy.ProtocolEgressHTTP, egressBackendHTTP),
			listener(lbproxy.ProtocolEgressTLS, egressBackendTLS),
			listener(lbproxy.ProtocolEgressProxy, egressBackendExplicit),
		},
		TargetGroups: []lbproxy.TargetGroup{},
	}
}

// ExecEgress is the EgressBackend driving iproute2 and iptables.
type ExecEgress struct {
	run Runner
}

// NewExecEgress returns an ExecEgress using the real commands.
func NewExecEgress() *ExecEgress { return &ExecEgress{run: defaultRun} }

// NewExecEgressWithRunner returns an ExecEgress driven by run, for tests.
func NewExecEgressWithRunner(run Runner) *ExecEgress { return &ExecEgress{run: run} }

// EnsureEgress implements EgressBackend.
func (b *ExecEgress) EnsureEgress(ctx context.Context, e EgressSetup) error {
	ns := lbNamespaces{run: b.run}
	pid, err := ns.pid(ctx, e.VPCID, true)
	if err != nil {
		return err
	}
	in := ns.in(pid)
	if err := ensureVIP(ctx, b.run, in, e.VPCID, e.Bridge, e.VIP); err != nil {
		return err
	}
	if err := ensureHostLeg(ctx, b.run, in, pid, e.VPCID); err != nil {
		return err
	}
	// The proxies go out as the host. Every namespace's leg has the same
	// address, so one rule covers them all.
	if err := iptablesEnsure(ctx, b.run, "nat", []string{"POSTROUTING", "-s", lbPublicNS + "/32", "-o", e.Uplink, "-j", "MASQUERADE"}); err != nil {
		return err
	}

	// The rules are written as `iptables -S` prints them back, so syncChain
	// finds them unchanged: CIDRs in canonical form, protocol matches named.
	allowed := make([]string, 0, len(e.Allowed))
	for _, a := range e.Allowed {
		n, err := a.Network()
		if err != nil {
			return err
		}
		allowed = append(allowed, n.String())
	}
	natChain, filterChain := egressChains(e.VPCID)
	redirect := [][]string{}
	for _, c := range append(slices.Clone(privateBlocks), allowed...) {
		redirect = append(redirect, []string{"-d", c, "-j", "RETURN"})
	}
	redirect = append(redirect,
		[]string{"-p", "tcp", "-m", "tcp", "--dport", "80", "-j", "DNAT", "--to-destination", net.JoinHostPort(e.VIP, strconv.Itoa(egressPortHTTP))},
		[]string{"-p", "tcp", "-m", "tcp", "--dport", "443", "-j", "DNAT", "--to-destination", net.JoinHostPort(e.VIP, strconv.Itoa(egressPortTLS))},
	)
	if err := syncChain(ctx, b.run, "nat", natChain, redirect); err != nil {
		return err
	}
	if err := iptablesEnsure(ctx, b.run, "nat", []string{"PREROUTING", "-i", e.Bridge, "-j", natChain}); err != nil {
		return err
	}

	filter := [][]string{}
	for i, a := range e.Allowed {
		rule := []string{"-d", allowed[i]}
		if a.Protocol == "tcp" || a.Protocol == "udp" {
			rule = append(rule, "-p", a.Protocol)
			if a.Port != 0 {
				rule = append(rule, "-m", a.Protocol, "--dport", strconv.Itoa(a.Port))
			}
		}
		filter = append(filter, append(rule, "-j", "ACCEPT"))
	}
	filter = append(filter, []string{"-j", "REJECT", "--reject-with", "icmp-admin-prohibited"})
	if err := syncChain(ctx, b.run, "filter", filterChain, filter); err != nil {
		return err
	}
	// First in FORWARD, ahead of anything accepting the connection.
	link := []string{"FORWARD", "-o", e.Uplink, "-m", "connmark", "--mark", hex32(vrfMark(e.VPCID)),
		"-m", "conntrack", "--ctstate", "NEW", "-j", filterChain}
	if _, err := b.run(ctx, "iptables", append([]string{"-C"}, link...)...); err != nil {
		if out, err := b.run(ctx, "iptables", append([]string{"-I", "FORWARD", "1"}, link[1:]...)...); err != nil {
			return fmt.Errorf("manager: link %s: %w: %s", filterChain, err, strings.TrimSpace(out))
		}
	}
	return nil
}

// syncChain makes chain in table hold exactly rules, rewriting it only when
// it differs, so a pass that changes nothing leaves no gap.
func syncChain(ctx context.Context, run Runner, table, chain string, rules [][]string) error {
	_, _ = run(ctx, "iptables", "-t", table, "-N", chain)
	want := make([]string, 0, len(rules)+1)
	want = append(want, "-N "+chain)
	for _, r := range rules {
		want = append(want, "-A "+chain+" "+strings.Join(r, " "))
	}
	out, err := run(ctx, "iptables", "-t", table, "-S", chain)
	if err == nil && slices.Equal(strings.Split(strings.TrimSpace(out), "\n"), want) {
		return nil
	}
	if out, err := run(ctx, "iptables", "-t", table, "-F", chain); err != nil {
		return fmt.Errorf("manager: flush %s: %w: %s", chain, err, strings.TrimSpace(out))
	}
	for _, r := range rules {
		if out, err := run(ctx, "iptables", append([]string{"-t", table, "-A", chain}, r...)...); err != nil {
			return fmt.Errorf("manager: %s %v: %w: %s", chain, r, err, strings.TrimSpace(out))
		}
	}
	return nil
}

// Prune implements EgressBackend: it finds the egress chains by name, so it
// also removes those of VPCs an earlier agent run filtered.
func (b *ExecEgress) Prune(ctx context.Context, keep []string) error {
	kept := map[string]bool{}
	for _, id := range keep {
		n, f := egressChains(id)
		kept[n], kept[f] = true, true
	}
	var errs []error
	for _, t := range []struct{ table, prefix, from string }{
		{"nat", "INFRA-EGR-", "PREROUTING"},
		{"filter", "INFRA-EGF-", "FORWARD"},
	} {
		out, err := b.run(ctx, "iptables", "-t", t.table, "-S")
		if err != nil {
			errs = append(errs, fmt.Errorf("manager: list %s rules: %w", t.table, err))
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			chain, ok := strings.CutPrefix(strings.TrimSpace(line), "-N ")
			if !ok || !strings.HasPrefix(chain, t.prefix) || kept[chain] {
				continue
			}
			for _, l := range strings.Split(out, "\n") {
				f := strings.Fields(l)
				if len(f) > 2 && f[0] == "-A" && f[1] == t.from && strings.HasSuffix(l, "-j "+chain) {
					_, _ = b.run(ctx, "iptables", append([]string{"-t", t.table, "-D"}, f[1:]...)...)
				}
			}
			_, _ = b.run(ctx, "iptables", "-t", t.table, "-F", chain)
			if out, err := b.run(ctx, "iptables", "-t", t.table, "-X", chain); err != nil {
				errs = append(errs, fmt.Errorf("manager: delete %s: %w: %s", chain, err, strings.TrimSpace(out)))
			}
		}
	}
	return errors.Join(errs...)
}
