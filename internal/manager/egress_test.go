// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/lbproxy"
	"github.com/goabonga/infrastructure/internal/manager"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

type fakeEgress struct {
	ensured []manager.EgressSetup
	kept    []string
}

func (f *fakeEgress) EnsureEgress(_ context.Context, e manager.EgressSetup) error {
	f.ensured = append(f.ensured, e)
	return nil
}

func (f *fakeEgress) Prune(_ context.Context, keep []string) error {
	f.kept = keep
	return nil
}

type egressEnv struct {
	igws    *manager.IGWRegistry
	vpcs    *manager.VPCRegistry
	subnets *manager.SubnetRegistry
	nodes   *manager.NodeRegistry
	backend *fakeEgress
	plane   *fakePlane
}

func newEgressEnv(t *testing.T) *egressEnv {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	env := &egressEnv{
		igws:    registry.New[resource.IGWSpec, resource.IGWStatus](store, resource.KindIGW),
		vpcs:    registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC),
		subnets: registry.New[resource.SubnetSpec, resource.SubnetStatus](store, resource.KindSubnet),
		nodes:   registry.New[resource.NodeSpec, resource.NodeStatus](store, resource.KindNode),
		backend: &fakeEgress{},
		plane:   &fakePlane{applied: map[string]*lbproxy.Config{}, status: map[string]lbproxy.Status{}},
	}
	v := &resource.VPC{Metadata: resource.ObjectMeta{UID: "vpc1"}, Spec: resource.VPCSpec{CIDR: "10.20.0.0/16"}}
	v.Status.BridgeName = "br-vpc1"
	put(t, env.vpcs.Put(v))
	put(t, env.subnets.Put(&resource.Subnet{Metadata: resource.ObjectMeta{UID: "sn"}, Spec: resource.SubnetSpec{VPCID: "vpc1", CIDR: "10.20.1.0/24"}}))
	putNode(t, env.nodes, "node-a", "192.168.0.1")
	putNode(t, env.nodes, "node-b", "192.168.0.2")
	return env
}

func (env *egressEnv) putIGW(t *testing.T, p *resource.EgressProxySpec) {
	t.Helper()
	igw := &resource.IGW{Metadata: resource.ObjectMeta{UID: "igw", Generation: 1}, Spec: resource.IGWSpec{VPCID: "vpc1", EgressProxy: p}}
	igw.Status.HostIface = "enp1s0"
	put(t, env.igws.Put(igw))
}

func (env *egressEnv) reconciler() *manager.EgressReconciler {
	return manager.NewEgressReconciler(env.igws, env.vpcs, env.subnets, env.nodes, env.backend, env.plane, "node-b")
}

func TestEgressProxyServesTheVPCFromEveryHost(t *testing.T) {
	t.Parallel()

	env := newEgressEnv(t)
	env.putIGW(t, &resource.EgressProxySpec{Enabled: true, AllowedDomains: []string{"example.com"},
		AllowedAddresses: []resource.EgressAddress{{CIDR: "203.0.113.7", Protocol: "udp", Port: 123}}})
	r := env.reconciler()
	if err := r.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(env.backend.ensured) != 1 {
		t.Fatalf("ensured %+v", env.backend.ensured)
	}
	e := env.backend.ensured[0]
	if e.VIP != "10.20.0.2" || e.Uplink != "enp1s0" || e.Bridge != "br-vpc1" || len(e.Allowed) != 1 {
		t.Fatalf("setup %+v", e)
	}

	// This host's proxy: the three egress listeners on node-b's (rank 1)
	// address, resolving through the VPC's resolver.
	cfg := env.plane.applied["lb-vpc1"]
	if cfg == nil || len(cfg.Listeners) != 3 {
		t.Fatalf("proxy config %+v", cfg)
	}
	for _, l := range cfg.Listeners {
		if l.Address != "10.20.1.253" || l.Egress == nil || l.Egress.Resolver != "10.20.0.1:53" ||
			!slices.Equal(l.Egress.AllowedDomains, []string{"example.com"}) || !slices.Equal(l.Egress.AllowedCIDRs, []string{"203.0.113.7"}) {
			t.Fatalf("proxy listener %+v", l)
		}
	}

	// The proxy address: each port balanced over both hosts' proxies.
	ls, gs := r.ExtraConfig("vpc1")
	var ports []int
	for _, l := range ls {
		ports = append(ports, l.Port)
		if l.Address != "10.20.0.2" || l.Protocol != "tcp" {
			t.Fatalf("front listener %+v", l)
		}
	}
	slices.Sort(ports)
	if !slices.Equal(ports, []int{80, 443, 3128}) {
		t.Fatalf("front ports %v", ports)
	}
	for _, g := range gs {
		var addrs []string
		for _, tg := range g.Targets {
			addrs = append(addrs, tg.Address)
		}
		if !slices.Equal(addrs, []string{"10.20.1.254", "10.20.1.253"}) || g.HealthCheck.Protocol != "tcp" {
			t.Fatalf("group %s targets %v", g.Name, addrs)
		}
	}
	if igw, _ := env.igws.Get("igw"); igw.Status.EgressProxy == nil || igw.Status.EgressProxy.Address != "10.20.0.2" {
		t.Fatalf("status %+v", igw.Status.EgressProxy)
	}
	if !slices.Equal(env.backend.kept, []string{"vpc1"}) {
		t.Fatalf("kept %v", env.backend.kept)
	}
}

func TestDisablingTheEgressProxyRemovesIt(t *testing.T) {
	t.Parallel()

	env := newEgressEnv(t)
	env.putIGW(t, &resource.EgressProxySpec{Enabled: true})
	r := env.reconciler()
	_ = r.ReconcileAll(context.Background())

	igw, _ := env.igws.Get("igw")
	igw.Spec.EgressProxy.Enabled = false
	put(t, env.igws.Put(igw))
	if err := r.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(env.backend.kept) != 0 || !slices.Equal(env.plane.stopped, []string{"lb-vpc1"}) {
		t.Fatalf("kept %v, stopped %v", env.backend.kept, env.plane.stopped)
	}
	if ls, _ := r.ExtraConfig("vpc1"); len(ls) != 0 {
		t.Fatalf("front listeners left %v", ls)
	}
	if igw, _ := env.igws.Get("igw"); igw.Status.EgressProxy != nil {
		t.Fatalf("status %+v", igw.Status.EgressProxy)
	}
}

func TestExecEgressRedirectsAndFilters(t *testing.T) {
	t.Parallel()

	h := &nsHost{}
	// No rule is in place yet, in any table.
	run := func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "iptables" && slices.Contains(args, "-C") {
			h.calls = append(h.calls, name+" "+strings.Join(args, " "))
			return "", errors.New("exit status 1")
		}
		return h.run(ctx, name, args...)
	}
	err := manager.NewExecEgressWithRunner(run).EnsureEgress(context.Background(), manager.EgressSetup{
		VPCID: "vpc1", Bridge: "br-vpc1", VIP: "10.20.0.2", Uplink: "enp1s0",
		Allowed: []resource.EgressAddress{{CIDR: "203.0.113.7", Protocol: "udp", Port: 123}, {CIDR: "198.51.100.0/24"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := h.joined()
	for _, want := range []string{
		inNS + "ip addr replace 10.20.0.2/32 dev la0",
		"ip link add lp-vpc1 mtu 1450 type veth peer name pub0 mtu 1450 netns 4242",
		"iptables -t nat -A POSTROUTING -s 169.254.0.2/32 -o enp1s0 -j MASQUERADE",
		"-A INFRA-EGR-",
		"-d 10.0.0.0/8 -j RETURN",
		"-d 203.0.113.7/32 -j RETURN",
		"-p tcp -m tcp --dport 443 -j DNAT --to-destination 10.20.0.2:443",
		"iptables -t nat -A PREROUTING -i br-vpc1 -j INFRA-EGR-",
		"-d 203.0.113.7/32 -p udp -m udp --dport 123 -j ACCEPT",
		"-d 198.51.100.0/24 -j ACCEPT",
		"-j REJECT --reject-with icmp-admin-prohibited",
		"iptables -I FORWARD 1 -o enp1s0 -m connmark --mark ",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in\n%s", want, joined)
		}
	}
}

func TestExecEgressLeavesAnUnchangedChainAlone(t *testing.T) {
	t.Parallel()

	setup := manager.EgressSetup{VPCID: "vpc1", Bridge: "br-vpc1", VIP: "10.20.0.2", Uplink: "enp1s0"}
	// A first pass records the chains as written; a host answering -S with
	// them must see no flush.
	first := &nsHost{}
	_ = manager.NewExecEgressWithRunner(first.run).EnsureEgress(context.Background(), setup)
	chains := map[string][]string{}
	for _, c := range first.calls {
		f := strings.Fields(c)
		if len(f) > 4 && f[0] == "iptables" && f[3] == "-A" && strings.HasPrefix(f[4], "INFRA-EG") {
			chains[f[4]] = append(chains[f[4]], strings.Join(f[3:], " "))
		}
	}
	var flushed []string
	run := func(ctx context.Context, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		if len(args) == 4 && args[2] == "-S" {
			return "-N " + args[3] + "\n" + strings.Join(chains[args[3]], "\n") + "\n", nil
		}
		if len(args) == 4 && args[2] == "-F" {
			flushed = append(flushed, cmd)
		}
		return (&nsHost{}).run(ctx, name, args...)
	}
	if err := manager.NewExecEgressWithRunner(run).EnsureEgress(context.Background(), setup); err != nil {
		t.Fatal(err)
	}
	if len(flushed) != 0 {
		t.Fatalf("unchanged chains were rewritten: %v", flushed)
	}
}

func TestExecEgressPrunesTheChainsOfOtherVPCs(t *testing.T) {
	t.Parallel()

	var calls []string
	run := func(_ context.Context, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		calls = append(calls, cmd)
		switch cmd {
		case "iptables -t nat -S":
			return "-P PREROUTING ACCEPT\n-N INFRA-EGR-deadbeef\n-A PREROUTING -i br-old -j INFRA-EGR-deadbeef\n", nil
		case "iptables -t filter -S":
			return "-P FORWARD ACCEPT\n", nil
		}
		return "", errors.New("unexpected")
	}
	_ = manager.NewExecEgressWithRunner(run).Prune(context.Background(), nil)
	for _, want := range []string{
		"iptables -t nat -D PREROUTING -i br-old -j INFRA-EGR-deadbeef",
		"iptables -t nat -F INFRA-EGR-deadbeef",
		"iptables -t nat -X INFRA-EGR-deadbeef",
	} {
		if !slices.Contains(calls, want) {
			t.Fatalf("missing %q in %v", want, calls)
		}
	}
}

func TestListenerPassServesWhatItsSourcesAdd(t *testing.T) {
	t.Parallel()

	env := newL7Env(t)
	src := staticListeners{ls: []lbproxy.Listener{{Name: "egress", Address: "10.20.0.2", Port: 3128, Protocol: "tcp", DefaultTargetGroup: "g"}},
		gs: []lbproxy.TargetGroup{{Name: "g", Protocol: "tcp", Targets: []lbproxy.Target{{ID: "a", Address: "10.20.1.254", Port: 13128, Weight: 1}},
			HealthCheck: lbproxy.HealthCheck{Protocol: "tcp", IntervalSeconds: 2, TimeoutSeconds: 1, HealthyThreshold: 2, UnhealthyThreshold: 2}}}}
	if err := env.reconciler().WithSources(src).ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cfg := env.plane.applied["lb-vpc1"]; cfg == nil || len(cfg.Listeners) != 1 || cfg.Listeners[0].Name != "egress" {
		t.Fatalf("config %+v", cfg)
	}
}

type staticListeners struct {
	ls []lbproxy.Listener
	gs []lbproxy.TargetGroup
}

func (s staticListeners) ExtraConfig(string) ([]lbproxy.Listener, []lbproxy.TargetGroup) {
	return s.ls, s.gs
}
