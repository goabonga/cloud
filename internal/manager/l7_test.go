// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/lbproxy"
	"github.com/goabonga/infrastructure/internal/manager"
	"github.com/goabonga/infrastructure/internal/pki"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

type fakePlane struct {
	applied map[string]*lbproxy.Config
	stopped []string
	status  map[string]lbproxy.Status
}

func (p *fakePlane) Apply(_ context.Context, ns string, cfg *lbproxy.Config) error {
	p.applied[ns] = cfg
	return nil
}

func (p *fakePlane) Stop(_ context.Context, ns string) error {
	delete(p.applied, ns)
	p.stopped = append(p.stopped, ns)
	return nil
}

func (p *fakePlane) Status(ns string) (lbproxy.Status, bool) {
	st, ok := p.status[ns]
	return st, ok
}

func (p *fakePlane) Serving() []string {
	var out []string
	for ns := range p.applied {
		out = append(out, ns)
	}
	return out
}

// fakeTLS serves real PEMs, as infra-lb validates them: "web-cert" is
// issued by "ca-internal", "late-cert" is not issued yet.
type fakeTLS struct {
	caPEM, certPEM, keyPEM []byte
}

func newFakeTLS(t *testing.T) *fakeTLS {
	t.Helper()
	ca, err := pki.NewCA(pki.CASpec{CommonName: "test ca", ValidFor: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	cert, key, err := ca.Issue(pki.CertSpec{CommonName: "www.demo.test", DNSNames: []string{"www.demo.test"}, ValidFor: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return &fakeTLS{caPEM: ca.CertPEM, certPEM: cert, keyPEM: key}
}

func (f *fakeTLS) CertificatePart(id, part string) ([]byte, error) {
	switch {
	case id == "late-cert":
		return nil, manager.ErrCertificatePending
	case id != "web-cert":
		return nil, errors.New("no such certificate")
	case part == resource.SSLPartPrivateKey:
		return f.keyPEM, nil
	}
	return append(slices.Clone(f.certPEM), f.caPEM...), nil
}

func (f *fakeTLS) CACertificate(string) ([]byte, error) { return f.caPEM, nil }

type l7Env struct {
	listeners *manager.LBListenerRegistry
	groups    *manager.LBTargetGroupRegistry
	targets   *manager.LBTargetRegistry
	lbs       *manager.LoadBalancerRegistry
	computes  *manager.ComputeRegistry
	vpcs      *manager.VPCRegistry
	plane     *fakePlane
	tls       *fakeTLS
}

func newL7Env(t *testing.T) *l7Env {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	env := &l7Env{
		listeners: registry.New[resource.LBListenerSpec, resource.LBListenerStatus](store, resource.KindLBListener),
		groups:    registry.New[resource.LBTargetGroupSpec, resource.LBTargetGroupStatus](store, resource.KindLBTargetGroup),
		targets:   registry.New[resource.LBTargetSpec, resource.LBTargetStatus](store, resource.KindLBTarget),
		lbs:       registry.New[resource.LoadBalancerSpec, resource.LoadBalancerStatus](store, resource.KindLoadBalancer),
		computes:  registry.New[resource.ComputeSpec, resource.ComputeStatus](store, resource.KindCompute),
		vpcs:      registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC),
		plane:     &fakePlane{applied: map[string]*lbproxy.Config{}, status: map[string]lbproxy.Status{}},
		tls:       newFakeTLS(t),
	}
	v := &resource.VPC{Metadata: resource.ObjectMeta{UID: "vpc1", Generation: 1}, Spec: resource.VPCSpec{CIDR: "10.20.0.0/16"}}
	v.Status.BridgeName = "br-vpc1"
	put(t, env.vpcs.Put(v))
	lb := &resource.LoadBalancer{Metadata: resource.ObjectMeta{UID: "lb", Generation: 1}, Spec: resource.LoadBalancerSpec{VPCID: "vpc1"}}
	lb.Status.Address, lb.Status.PublicAddress = "10.20.0.10", "203.0.113.10"
	put(t, env.lbs.Put(lb))
	for name, ip := range map[string]string{"web-1": "10.20.1.10", "web-2": "10.20.1.11"} {
		c := &resource.Compute{Metadata: resource.ObjectMeta{UID: name, Generation: 1}, Spec: resource.ComputeSpec{SubnetID: "sn", Image: "nginx"}}
		c.Status.IP = ip
		put(t, env.computes.Put(c))
		put(t, env.targets.Put(&resource.LBTarget{Metadata: resource.ObjectMeta{UID: "t-" + name, Generation: 1},
			Spec: resource.LBTargetSpec{TargetGroupID: "tg", ComputeID: name}}))
	}
	put(t, env.groups.Put(&resource.LBTargetGroup{Metadata: resource.ObjectMeta{UID: "tg", Generation: 1},
		Spec: resource.LBTargetGroupSpec{VPCID: "vpc1", Protocol: "https", Port: 443, BackendCAID: "ca-internal", ServerName: "web.internal.demo"}}))
	return env
}

func put(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (env *l7Env) putListener(t *testing.T, uid string, port int, certs ...string) {
	t.Helper()
	put(t, env.listeners.Put(&resource.LBListener{Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec: resource.LBListenerSpec{LoadBalancerID: "lb", Port: port, Protocol: "https", TLSMode: "reencrypt",
			CertificateIDs: certs, DefaultTargetGroupID: "tg"}}))
}

func (env *l7Env) reconciler() *manager.ListenerReconciler {
	return manager.NewListenerReconciler(env.listeners, env.groups, env.targets, env.lbs, env.computes, env.vpcs, env.plane, env.tls, "node-a")
}

func TestListenersServeTheVIPAndThePublicAddressOnTheEdges(t *testing.T) {
	t.Parallel()

	env := newL7Env(t)
	env.putListener(t, "https", 443, "web-cert")
	if err := env.reconciler().AsEdge().ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := env.plane.applied["lb-vpc1"]
	if cfg == nil || len(cfg.Listeners) != 2 {
		t.Fatalf("config %+v", cfg)
	}
	var addrs []string
	for _, l := range cfg.Listeners {
		addrs = append(addrs, l.Address)
		if l.Port != 443 || l.TLSMode != "reencrypt" || len(l.Certificates) != 1 ||
			!strings.HasPrefix(l.Certificates[0].CertPEM, string(env.tls.certPEM)) || l.Certificates[0].KeyPEM != string(env.tls.keyPEM) {
			t.Fatalf("listener %+v", l)
		}
	}
	if !slices.Equal(addrs, []string{"10.20.0.10", "203.0.113.10"}) {
		t.Fatalf("addresses %v", addrs)
	}
	g := cfg.TargetGroups[0]
	if g.BackendCAPEM != string(env.tls.caPEM) || g.ServerName != "web.internal.demo" || len(g.Targets) != 2 ||
		g.Targets[0].Address != "10.20.1.10" || g.Targets[0].Port != 443 || g.HealthCheck.Path != "/" {
		t.Fatalf("target group %+v", g)
	}

	// A host that is no edge serves the VIP only.
	inner := newL7Env(t)
	inner.putListener(t, "https", 443, "web-cert")
	_ = inner.reconciler().ReconcileAll(context.Background())
	if l := inner.plane.applied["lb-vpc1"].Listeners; len(l) != 1 || l[0].Address != "10.20.0.10" {
		t.Fatalf("listeners %+v", l)
	}
}

func TestListenerWaitsForItsCertificateAndRefusesTheLayer4Port(t *testing.T) {
	t.Parallel()

	env := newL7Env(t)
	env.putListener(t, "late", 8443, "late-cert")
	lb, _ := env.lbs.Get("lb")
	lb.Spec.Port = 443
	put(t, env.lbs.Put(lb))
	env.putListener(t, "clash", 443, "web-cert")
	env.putListener(t, "ok", 9443, "web-cert")
	if err := env.reconciler().ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg := env.plane.applied["lb-vpc1"]
	if len(cfg.Listeners) != 1 || !strings.HasPrefix(cfg.Listeners[0].Name, "ok@") {
		t.Fatalf("listeners %+v", cfg.Listeners)
	}
	late, _ := env.listeners.Get("late")
	clash, _ := env.listeners.Get("clash")
	if late.Status.Phase != resource.PhasePending || clash.Status.Phase != resource.PhaseError {
		t.Fatalf("late %q, clash %q", late.Status.Phase, clash.Status.Phase)
	}
}

func TestListenerReportsBindingAndTargetHealthPerNode(t *testing.T) {
	t.Parallel()

	env := newL7Env(t)
	env.putListener(t, "https", 443, "web-cert")
	g, _ := env.groups.Get("tg")
	g.Status.Targets = []resource.LBTargetHealth{{ComputeID: "web-1", Health: "healthy", NodeName: "node-b"}}
	put(t, env.groups.Put(g))
	env.plane.status["lb-vpc1"] = lbproxy.Status{
		Listeners: []lbproxy.ListenerStatus{{Name: "https@10.20.0.10", Bound: true}},
		TargetGroups: []lbproxy.GroupStatus{{Name: "tg", Targets: []lbproxy.TargetStatus{
			{ID: "t-web-1", Address: "10.20.1.10", Port: 443, Health: "healthy"},
			{ID: "t-web-2", Address: "10.20.1.11", Port: 443, Health: "unhealthy"},
		}}},
	}
	if err := env.reconciler().ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if l, _ := env.listeners.Get("https"); l.Status.Phase != resource.PhaseReady {
		t.Fatalf("listener phase %q", l.Status.Phase)
	}
	g, _ = env.groups.Get("tg")
	var got []string
	for _, h := range g.Status.Targets {
		got = append(got, h.NodeName+"/"+h.ComputeID+"="+h.Health)
	}
	if !slices.Equal(got, []string{"node-a/web-1=healthy", "node-a/web-2=unhealthy", "node-b/web-1=healthy"}) {
		t.Fatalf("health %v", got)
	}
}

func TestListenerPassStopsANamespaceWithoutListeners(t *testing.T) {
	t.Parallel()

	env := newL7Env(t)
	env.plane.applied["lb-gone"] = &lbproxy.Config{}
	if err := env.reconciler().ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(env.plane.stopped, []string{"lb-gone"}) {
		t.Fatalf("stopped %v", env.plane.stopped)
	}
}

func TestExecDataPlaneStartsReloadsAndStops(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var calls []string
	active := false
	run := func(_ context.Context, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		calls = append(calls, cmd)
		switch {
		case strings.HasPrefix(cmd, "systemctl is-active") && !active:
			return "inactive", errors.New("exit status 3")
		case strings.HasPrefix(cmd, "systemctl start"):
			active = true
		}
		return "", nil
	}
	p := manager.NewExecDataPlaneWith(run, dir)
	cfg := &lbproxy.Config{Listeners: []lbproxy.Listener{{Name: "a", Address: "10.0.0.1", Port: 80, Protocol: "http", DefaultTargetGroup: "g"}}}
	ctx := context.Background()
	put(t, p.Apply(ctx, "lb-x", cfg))
	info, err := os.Stat(filepath.Join(dir, "lb-x", "config.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config file %v %v", info, err)
	}
	put(t, p.Apply(ctx, "lb-x", cfg))
	cfg.Listeners[0].Port = 8080
	put(t, p.Apply(ctx, "lb-x", cfg))
	var starts, reloads int
	for _, c := range calls {
		starts += strings.Count(c, "systemctl start infra-lb@lb-x.service")
		reloads += strings.Count(c, "systemctl reload infra-lb@lb-x.service")
	}
	if starts != 1 || reloads != 1 {
		t.Fatalf("starts %d reloads %d, want one each:\n%s", starts, reloads, strings.Join(calls, "\n"))
	}
	if !slices.Equal(p.Serving(), []string{"lb-x"}) {
		t.Fatalf("serving %v", p.Serving())
	}
	put(t, p.Stop(ctx, "lb-x"))
	if len(p.Serving()) != 0 {
		t.Fatal("stopping must forget the configuration")
	}
}
