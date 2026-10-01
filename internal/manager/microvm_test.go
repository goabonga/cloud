// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

type fakeMicroVMBackend struct {
	ensured   []manager.MicroVMRequest
	deleted   []manager.MicroVMTeardown
	ensureErr error
}

func (f *fakeMicroVMBackend) EnsureMicroVM(_ context.Context, req manager.MicroVMRequest) (manager.MicroVMResult, error) {
	if f.ensureErr != nil {
		return manager.MicroVMResult{}, f.ensureErr
	}
	f.ensured = append(f.ensured, req)
	return manager.MicroVMResult{Tap: "tap-" + req.UID, Pid: 4242}, nil
}

func (f *fakeMicroVMBackend) DeleteMicroVM(_ context.Context, td manager.MicroVMTeardown) error {
	f.deleted = append(f.deleted, td)
	return nil
}

// microvmEnv holds the registries a micro-VM reconciler depends on, pre-seeded
// with a ready VPC (bridge) and a ready subnet (gateway).
type microvmEnv struct {
	vpcs     *manager.VPCRegistry
	subnets  *manager.SubnetRegistry
	sgs      *manager.SecurityGroupRegistry
	microvms *manager.MicroVMRegistry
	store    state.Store
}

func newMicroVMEnv(t *testing.T) *microvmEnv {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	env := &microvmEnv{
		vpcs:     registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC),
		subnets:  registry.New[resource.SubnetSpec, resource.SubnetStatus](store, resource.KindSubnet),
		sgs:      registry.New[resource.SecurityGroupSpec, resource.SecurityGroupStatus](store, resource.KindSecurityGroup),
		microvms: registry.New[resource.MicroVMSpec, resource.MicroVMStatus](store, resource.KindMicroVM),
		store:    store,
	}
	v := &resource.VPC{Metadata: resource.ObjectMeta{UID: "vpc-1", Generation: 1}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	v.Status.BridgeName = "br-vpc1"
	if err := env.vpcs.Put(v); err != nil {
		t.Fatalf("seed vpc: %v", err)
	}
	sn := &resource.Subnet{Metadata: resource.ObjectMeta{UID: "sn-1", Generation: 1}, Spec: resource.SubnetSpec{VPCID: "vpc-1", CIDR: "10.0.1.0/24", Type: "public"}}
	sn.Status.Gateway = "10.0.1.1"
	if err := env.subnets.Put(sn); err != nil {
		t.Fatalf("seed subnet: %v", err)
	}
	return env
}

func (env *microvmEnv) reconciler(be manager.MicroVMBackend) *manager.MicroVMReconciler {
	return manager.NewMicroVMReconciler(env.microvms, env.subnets, env.vpcs, env.sgs, be, "")
}

func (env *microvmEnv) putMicroVM(t *testing.T, v *resource.MicroVM) {
	t.Helper()
	if err := env.microvms.Put(v); err != nil {
		t.Fatalf("seed microvm: %v", err)
	}
}

func basicMicroVM(uid string) *resource.MicroVM {
	return &resource.MicroVM{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec: resource.MicroVMSpec{
			SubnetID:   "sn-1",
			VCPUs:      1,
			MemoryMB:   512,
			KernelPath: "/boot/vmlinux",
			Image:      "/images/base.raw",
		},
	}
}

func TestMicroVMReconcileSuccess(t *testing.T) {
	t.Parallel()

	env := newMicroVMEnv(t)
	sg := &resource.SecurityGroup{Metadata: resource.ObjectMeta{UID: "sg-1", Generation: 1}, Spec: resource.SecurityGroupSpec{VPCID: "vpc-1"}}
	sg.Status.Chain = "INFRA-SG-AB"
	if err := env.sgs.Put(sg); err != nil {
		t.Fatalf("seed sg: %v", err)
	}
	v := basicMicroVM("vm-1")
	v.Spec.SecurityGroupID = "sg-1"
	env.putMicroVM(t, v)

	be := &fakeMicroVMBackend{}
	rec := env.reconciler(be)
	if err := rec.Reconcile(context.Background(), "vm-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got, _ := env.microvms.Get("vm-1")
	if !got.Status.IsReady() || !got.Status.Ready {
		t.Fatalf("status not ready: %+v", got.Status)
	}
	if got.Status.Tap == "" || got.Status.Pid == 0 {
		t.Fatalf("topology not recorded: %+v", got.Status)
	}
	if len(be.ensured) != 1 {
		t.Fatalf("expected one EnsureMicroVM call, got %d", len(be.ensured))
	}
	req := be.ensured[0]
	if req.Bridge != "br-vpc1" || req.Gateway != "10.0.1.1" || req.Prefix != 24 {
		t.Fatalf("request not resolved: %+v", req)
	}
	if req.SGChain != "INFRA-SG-AB" {
		t.Fatalf("sg chain = %q", req.SGChain)
	}
	if req.KernelPath != "/boot/vmlinux" || req.Image != "/images/base.raw" {
		t.Fatalf("boot inputs not carried over: %+v", req)
	}
}

func TestMicroVMNodeScoping(t *testing.T) {
	t.Parallel()

	env := newMicroVMEnv(t)
	mine := basicMicroVM("vm-mine")
	mine.Status.NodeName = "node-a"
	env.putMicroVM(t, mine)
	other := basicMicroVM("vm-other")
	other.Status.NodeName = "node-b"
	env.putMicroVM(t, other)

	be := &fakeMicroVMBackend{}
	rec := manager.NewMicroVMReconciler(env.microvms, env.subnets, env.vpcs, env.sgs, be, "node-a")
	for _, uid := range []string{"vm-mine", "vm-other"} {
		if err := rec.Reconcile(context.Background(), uid); err != nil {
			t.Fatalf("reconcile %s: %v", uid, err)
		}
	}
	if len(be.ensured) != 1 || be.ensured[0].UID != "vm-mine" {
		t.Fatalf("only microvm scheduled to node-a should be realized: %+v", be.ensured)
	}
	if got, _ := env.microvms.Get("vm-other"); got.Status.Ready {
		t.Fatal("microvm on another node should be left alone")
	}
}

func TestMicroVMAllocatesDistinctIPs(t *testing.T) {
	t.Parallel()

	env := newMicroVMEnv(t)
	env.putMicroVM(t, basicMicroVM("vm-1"))
	env.putMicroVM(t, basicMicroVM("vm-2"))
	be := &fakeMicroVMBackend{}
	rec := env.reconciler(be)
	for _, uid := range []string{"vm-1", "vm-2"} {
		if err := rec.Reconcile(context.Background(), uid); err != nil {
			t.Fatalf("reconcile %s: %v", uid, err)
		}
	}
	a, _ := env.microvms.Get("vm-1")
	b, _ := env.microvms.Get("vm-2")
	if a.Status.IP == "" || b.Status.IP == "" || a.Status.IP == b.Status.IP {
		t.Fatalf("expected distinct IPs, got %q and %q", a.Status.IP, b.Status.IP)
	}
}

func TestMicroVMReservesAddressesAcrossAgents(t *testing.T) {
	t.Parallel()

	env := newMicroVMEnv(t)
	env.putMicroVM(t, basicMicroVM("vm-1"))
	env.putMicroVM(t, basicMicroVM("vm-2"))
	be := &fakeMicroVMBackend{}
	rec1 := env.reconciler(be).WithAddressStore(env.store)
	rec2 := env.reconciler(be).WithAddressStore(env.store)
	if err := rec1.Reconcile(context.Background(), "vm-1"); err != nil {
		t.Fatalf("reconcile vm-1: %v", err)
	}
	if err := rec2.Reconcile(context.Background(), "vm-2"); err != nil {
		t.Fatalf("reconcile vm-2: %v", err)
	}
	a, _ := env.microvms.Get("vm-1")
	b, _ := env.microvms.Get("vm-2")
	if a.Status.IP == "" || b.Status.IP == "" || a.Status.IP == b.Status.IP {
		t.Fatalf("expected distinct reserved IPs, got %q and %q", a.Status.IP, b.Status.IP)
	}
}

func TestMicroVMPendingDependencies(t *testing.T) {
	cases := []struct {
		name  string
		setup func(env *microvmEnv)
	}{
		{"subnet gateway missing", func(env *microvmEnv) {
			sn, _ := env.subnets.Get("sn-1")
			sn.Status.Gateway = ""
			_ = env.subnets.Put(sn)
		}},
		{"vpc bridge missing", func(env *microvmEnv) {
			v, _ := env.vpcs.Get("vpc-1")
			v.Status.BridgeName = ""
			_ = env.vpcs.Put(v)
		}},
		{"security group chain missing", func(env *microvmEnv) {
			sg := &resource.SecurityGroup{Metadata: resource.ObjectMeta{UID: "sg-1", Generation: 1}, Spec: resource.SecurityGroupSpec{VPCID: "vpc-1"}}
			_ = env.sgs.Put(sg) // Status.Chain empty
			v, _ := env.microvms.Get("vm-1")
			v.Spec.SecurityGroupID = "sg-1"
			_ = env.microvms.Put(v)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := newMicroVMEnv(t)
			env.putMicroVM(t, basicMicroVM("vm-1"))
			tc.setup(env)
			be := &fakeMicroVMBackend{}
			rec := env.reconciler(be)
			if err := rec.Reconcile(context.Background(), "vm-1"); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			got, _ := env.microvms.Get("vm-1")
			if got.Status.Phase != resource.PhasePending {
				t.Fatalf("phase = %q, want Pending", got.Status.Phase)
			}
			if len(be.ensured) != 0 {
				t.Fatalf("backend should not be called while pending: %+v", be.ensured)
			}
		})
	}
}

func TestMicroVMFinalize(t *testing.T) {
	t.Parallel()

	env := newMicroVMEnv(t)
	env.putMicroVM(t, basicMicroVM("vm-1"))
	be := &fakeMicroVMBackend{}
	rec := env.reconciler(be)
	if err := rec.Reconcile(context.Background(), "vm-1"); err != nil {
		t.Fatalf("reconcile up: %v", err)
	}

	v, _ := env.microvms.Get("vm-1")
	now := v.Metadata.CreatedAt
	v.Metadata.DeletionTimestamp = &now
	if err := env.microvms.Put(v); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}

	if err := rec.Reconcile(context.Background(), "vm-1"); err != nil {
		t.Fatalf("reconcile down: %v", err)
	}
	if len(be.deleted) != 1 || be.deleted[0].UID != "vm-1" {
		t.Fatalf("expected one DeleteMicroVM call: %+v", be.deleted)
	}
	if _, err := env.microvms.Get("vm-1"); err == nil {
		t.Fatal("microvm should be gone once finalizers clear")
	}
}

// tapSim is a minimal in-memory stand-in for `ip tuntap`/`ip link`/`iptables`,
// used to drive ExecMicroVMBackend's networking side effects without touching
// the kernel or shelling out to infra-hypervisor.
type tapSim struct {
	exist map[string]bool
	calls [][]string
}

func newTapSim(existing ...string) *tapSim {
	s := &tapSim{exist: make(map[string]bool)}
	for _, n := range existing {
		s.exist[n] = true
	}
	return s
}

func (s *tapSim) run(_ context.Context, name string, args ...string) (string, error) {
	s.calls = append(s.calls, append([]string{name}, args...))
	switch name {
	case "ip":
		switch {
		case len(args) >= 3 && args[0] == "link" && args[1] == "show":
			if s.exist[args[2]] {
				return args[2] + ": <BROADCAST> mtu 1500", nil
			}
			return "Cannot find device " + args[2], errors.New("exit status 1")
		case len(args) >= 5 && args[0] == "tuntap" && args[1] == "add":
			s.exist[args[3]] = true // ip tuntap add dev <tap> mode tap
			return "", nil
		case len(args) >= 4 && args[0] == "link" && args[1] == "set" && (args[3] == "up" || args[3] == "master"):
			return "", nil
		case len(args) >= 3 && args[0] == "link" && args[1] == "del":
			delete(s.exist, args[2])
			return "", nil
		default:
			return "", errors.New("unhandled ip args: " + strings.Join(args, " "))
		}
	case "iptables":
		if len(args) >= 1 && args[0] == "-C" {
			return "", errors.New("rule does not exist")
		}
		return "", nil
	default:
		return "", errors.New("unexpected command: " + name)
	}
}

// TestExecMicroVMBackendNetworkSetup exercises the TAP and security-group
// setup EnsureMicroVM does before resolving the boot image and handing off to
// infra-hypervisor. It expects an error back - the image path doesn't exist
// in this test - but the networking side effects must already have happened
// by then.
func TestExecMicroVMBackendNetworkSetup(t *testing.T) {
	t.Parallel()

	sim := newTapSim()
	dir := t.TempDir()
	be := manager.NewExecMicroVMBackendWithRunner(dir, sim.run)
	req := manager.MicroVMRequest{
		UID:        "vm-1",
		Bridge:     "br-vpc1",
		IP:         "10.0.1.10",
		Prefix:     24,
		Gateway:    "10.0.1.1",
		SGChain:    "INFRA-SG-AB",
		VCPUs:      1,
		MemoryMB:   512,
		KernelPath: "/boot/vmlinux",
		Image:      "/images/base.raw",
	}
	if _, err := be.EnsureMicroVM(context.Background(), req); err == nil {
		t.Fatal("expected an error: the image path does not exist in this test")
	}
	if !anyCallHas(sim.calls, "tuntap") {
		t.Fatalf("tap device not created: %v", sim.calls)
	}
	if !anyCallHas(sim.calls, "br-vpc1") {
		t.Fatalf("tap not attached to bridge: %v", sim.calls)
	}
	if !anyCallHas(sim.calls, "INFRA-SG-AB") {
		t.Fatalf("security group not attached: %v", sim.calls)
	}
}

func TestExecMicroVMBackendDelete(t *testing.T) {
	t.Parallel()

	sim := newTapSim("tap-vm1")
	dir := t.TempDir()
	be := manager.NewExecMicroVMBackendWithRunner(dir, sim.run)
	td := manager.MicroVMTeardown{UID: "vm-1", Tap: "tap-vm1", IP: "10.0.1.10", SGChain: "INFRA-SG-AB"}
	if err := be.DeleteMicroVM(context.Background(), td); err != nil {
		t.Fatalf("delete microvm: %v", err)
	}
	if !anyCallHas(sim.calls, "del") {
		t.Fatalf("expected tap deletion: %v", sim.calls)
	}
	if !anyCallHas(sim.calls, "-D") {
		t.Fatalf("expected iptables rule removal: %v", sim.calls)
	}
}

func TestMicroVMSubnetNotFound(t *testing.T) {
	t.Parallel()

	env := newMicroVMEnv(t)
	v := basicMicroVM("vm-1")
	v.Spec.SubnetID = "sn-missing"
	env.putMicroVM(t, v)

	be := &fakeMicroVMBackend{}
	rec := env.reconciler(be)
	if err := rec.Reconcile(context.Background(), "vm-1"); err == nil {
		t.Fatal("expected an error for a missing subnet")
	}
	got, _ := env.microvms.Get("vm-1")
	if got.Status.Phase != resource.PhaseError {
		t.Fatalf("phase = %q, want Error", got.Status.Phase)
	}
}
