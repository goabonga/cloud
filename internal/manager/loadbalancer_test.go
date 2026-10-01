// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

type fakeLBBackend struct {
	vip     string
	port    int
	bridge  string
	servers []manager.LBRealServer
	deleted bool
}

func (f *fakeLBBackend) EnsureService(_ context.Context, vip string, port int, _, _, bridge, _ string, servers []manager.LBRealServer) error {
	f.vip, f.port, f.bridge, f.servers = vip, port, bridge, servers
	return nil
}

func (f *fakeLBBackend) DeleteService(_ context.Context, _ string, _ int, _, _ string) error {
	f.deleted = true
	return nil
}

type lbEnv struct {
	lbs      *manager.LoadBalancerRegistry
	backends *manager.LBBackendRegistry
	computes *manager.ComputeRegistry
	vpcs     *manager.VPCRegistry
	ips      *manager.IPAddressRegistry
}

func newLBEnv(t *testing.T) *lbEnv {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	env := &lbEnv{
		lbs:      registry.New[resource.LoadBalancerSpec, resource.LoadBalancerStatus](store, resource.KindLoadBalancer),
		backends: registry.New[resource.LBBackendSpec, resource.LBBackendStatus](store, resource.KindLBBackend),
		computes: registry.New[resource.ComputeSpec, resource.ComputeStatus](store, resource.KindCompute),
		vpcs:     registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC),
		ips:      registry.New[resource.IPAddressSpec, resource.IPAddressStatus](store, resource.KindIPAddress),
	}
	v := &resource.VPC{Metadata: resource.ObjectMeta{UID: "vpc-1", Generation: 1}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	v.Status.BridgeName = "br-vpc1"
	if err := env.vpcs.Put(v); err != nil {
		t.Fatalf("seed vpc: %v", err)
	}
	return env
}

func (env *lbEnv) reconciler(be manager.LoadBalancerBackend) *manager.LoadBalancerReconciler {
	return manager.NewLoadBalancerReconciler(env.lbs, env.backends, env.computes, env.vpcs, be)
}

func (env *lbEnv) putCompute(t *testing.T, uid, ip string) {
	t.Helper()
	c := &resource.Compute{Metadata: resource.ObjectMeta{UID: uid, Generation: 1}, Spec: resource.ComputeSpec{SubnetID: "sn-1", Image: "x"}}
	c.Status.IP = ip
	if err := env.computes.Put(c); err != nil {
		t.Fatalf("seed compute: %v", err)
	}
}

func (env *lbEnv) putLB(t *testing.T, uid, address string) {
	t.Helper()
	if err := env.lbs.Put(&resource.LoadBalancer{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.LoadBalancerSpec{VPCID: "vpc-1", Address: address, Port: 443, Protocol: "tcp", Algorithm: "round_robin"},
	}); err != nil {
		t.Fatalf("seed lb: %v", err)
	}
}

func (env *lbEnv) putBackend(t *testing.T, uid, lbID, computeID string, port, weight int) {
	t.Helper()
	if err := env.backends.Put(&resource.LBBackend{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.LBBackendSpec{LBID: lbID, ComputeID: computeID, Port: port, Weight: weight},
	}); err != nil {
		t.Fatalf("seed backend: %v", err)
	}
}

func TestLBReconcileBuildsService(t *testing.T) {
	t.Parallel()

	env := newLBEnv(t)
	env.putCompute(t, "i-1", "10.0.1.10")
	env.putCompute(t, "i-2", "10.0.1.11")
	env.putLB(t, "lb-1", "")
	env.putBackend(t, "be-1", "lb-1", "i-1", 8080, 10)
	env.putBackend(t, "be-2", "lb-1", "i-2", 8080, 5)

	be := &fakeLBBackend{}
	rec := env.reconciler(be)
	if err := rec.Reconcile(context.Background(), "lb-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got, _ := env.lbs.Get("lb-1")
	if !got.Status.IsReady() {
		t.Fatalf("phase = %q, want Ready", got.Status.Phase)
	}
	if got.Status.Address != "10.0.0.10" {
		t.Fatalf("vip = %q, want 10.0.0.10", got.Status.Address)
	}
	if got.Status.ServiceID != "10.0.0.10:443" {
		t.Fatalf("serviceId = %q", got.Status.ServiceID)
	}
	if !got.Metadata.HasFinalizer(resource.LoadBalancerFinalizer) {
		t.Fatal("finalizer should be attached")
	}
	if be.bridge != "br-vpc1" || len(be.servers) != 2 {
		t.Fatalf("unexpected backend call: bridge=%q servers=%+v", be.bridge, be.servers)
	}
	ips := map[string]int{}
	for _, s := range be.servers {
		ips[s.IP] = s.Weight
	}
	if ips["10.0.1.10"] != 10 || ips["10.0.1.11"] != 5 {
		t.Fatalf("real servers not resolved: %+v", be.servers)
	}
	b1, _ := env.backends.Get("be-1")
	if b1.Status.RealServerIP != "10.0.1.10" || !b1.Status.IsReady() {
		t.Fatalf("backend status not updated: %+v", b1.Status)
	}
	if rec.Name() != resource.KindLoadBalancer {
		t.Fatalf("name = %q", rec.Name())
	}
}

func TestLBUsesSpecAddress(t *testing.T) {
	t.Parallel()

	env := newLBEnv(t)
	env.putLB(t, "lb-1", "10.0.5.5")
	be := &fakeLBBackend{}
	if err := env.reconciler(be).Reconcile(context.Background(), "lb-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _ := env.lbs.Get("lb-1")
	if got.Status.Address != "10.0.5.5" || be.vip != "10.0.5.5" {
		t.Fatalf("spec address not honored: status=%q vip=%q", got.Status.Address, be.vip)
	}
}

func TestLBSkipsUnreadyBackends(t *testing.T) {
	t.Parallel()

	env := newLBEnv(t)
	env.putCompute(t, "i-1", "10.0.1.10")
	env.putCompute(t, "i-2", "") // not ready
	env.putLB(t, "lb-1", "10.0.5.5")
	env.putBackend(t, "be-1", "lb-1", "i-1", 8080, 1)
	env.putBackend(t, "be-2", "lb-1", "i-2", 8080, 1)

	be := &fakeLBBackend{}
	if err := env.reconciler(be).Reconcile(context.Background(), "lb-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(be.servers) != 1 || be.servers[0].IP != "10.0.1.10" {
		t.Fatalf("only ready backends should be added: %+v", be.servers)
	}
	got, _ := env.lbs.Get("lb-1")
	if !got.Status.IsReady() {
		t.Fatalf("lb should be Ready, got %q", got.Status.Phase)
	}
}

func TestLBPendingWhenBridgeMissing(t *testing.T) {
	t.Parallel()

	env := newLBEnv(t)
	v, _ := env.vpcs.Get("vpc-1")
	v.Status.BridgeName = ""
	if err := env.vpcs.Put(v); err != nil {
		t.Fatalf("update vpc: %v", err)
	}
	env.putLB(t, "lb-1", "10.0.5.5")
	be := &fakeLBBackend{}
	if err := env.reconciler(be).Reconcile(context.Background(), "lb-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _ := env.lbs.Get("lb-1")
	if got.Status.Phase != resource.PhasePending {
		t.Fatalf("phase = %q, want Pending", got.Status.Phase)
	}
	if be.vip != "" {
		t.Fatal("backend should not be called while pending")
	}
}

func TestLBVPCNotFound(t *testing.T) {
	t.Parallel()

	env := newLBEnv(t)
	if err := env.lbs.Put(&resource.LoadBalancer{Metadata: resource.ObjectMeta{UID: "lb-1", Generation: 1}, Spec: resource.LoadBalancerSpec{VPCID: "missing", Port: 80}}); err != nil {
		t.Fatalf("seed lb: %v", err)
	}
	if err := env.reconciler(&fakeLBBackend{}).Reconcile(context.Background(), "lb-1"); err == nil {
		t.Fatal("expected an error when the VPC is missing")
	}
	got, _ := env.lbs.Get("lb-1")
	if got.Status.Phase != resource.PhaseError {
		t.Fatalf("phase = %q, want Error", got.Status.Phase)
	}
}

func TestLBFinalize(t *testing.T) {
	t.Parallel()

	env := newLBEnv(t)
	env.putLB(t, "lb-1", "10.0.5.5")
	be := &fakeLBBackend{}
	rec := env.reconciler(be)
	if err := rec.Reconcile(context.Background(), "lb-1"); err != nil {
		t.Fatalf("reconcile up: %v", err)
	}

	cur, _ := env.lbs.Get("lb-1")
	now := time.Now()
	cur.Metadata.DeletionTimestamp = &now
	if err := env.lbs.Put(cur); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}
	if err := rec.Reconcile(context.Background(), "lb-1"); err != nil {
		t.Fatalf("reconcile delete: %v", err)
	}
	if _, err := env.lbs.Get("lb-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("record should be removed, got %v", err)
	}
	if !be.deleted {
		t.Fatal("service should have been deleted")
	}
}

func TestExecLBEnsureAndDelete(t *testing.T) {
	t.Parallel()

	rec := &fwRecorder{}
	be := manager.NewExecLBWithRunner(rec.run)
	servers := []manager.LBRealServer{{IP: "10.0.1.10", Port: 8080, Weight: 3}}
	if err := be.EnsureService(context.Background(), "10.0.5.5", 443, "tcp", "round_robin", "br-vpc1", "np-vpc1", servers); err != nil {
		t.Fatalf("ensure service: %v", err)
	}
	for _, want := range []string{"ipvsadm", "-A", "-a", "10.0.5.5/32"} {
		if !anyCallHas(rec.calls, want) {
			t.Fatalf("missing %q in calls: %v", want, rec.calls)
		}
	}

	rec.calls = nil
	if err := be.DeleteService(context.Background(), "10.0.5.5", 443, "tcp", "br-vpc1"); err != nil {
		t.Fatalf("delete service: %v", err)
	}
	if !anyCallHas(rec.calls, "-D") {
		t.Fatalf("expected service deletion: %v", rec.calls)
	}
}

func TestExecLBMasqueradesThroughTheNodePort(t *testing.T) {
	t.Parallel()

	var calls []string
	run := func(_ context.Context, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		calls = append(calls, cmd)
		if strings.HasPrefix(cmd, "iptables -t nat -C") {
			return "", errors.New("rule does not exist")
		}
		return "", nil
	}
	servers := []manager.LBRealServer{{IP: "10.0.1.10", Port: 80, Weight: 1}}
	if err := manager.NewExecLBWithRunner(run).EnsureService(context.Background(), "10.0.5.5", 80, "tcp", "round_robin", "br-1", "np-1", servers); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{
		"sysctl -w net.ipv4.vs.conntrack=1",
		"iptables -t nat -A POSTROUTING -o np-1 -m ipvs --ipvs -j MASQUERADE",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in\n%s", want, joined)
		}
	}
	if strings.Index(joined, "ipvsadm -A") > strings.Index(joined, "net.ipv4.vs.conntrack") {
		t.Fatalf("the ipvs sysctl only exists once ip_vs is loaded by the service:\n%s", joined)
	}
}
