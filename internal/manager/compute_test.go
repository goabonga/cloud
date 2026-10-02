// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

type fakeComputeBackend struct {
	ensured   []manager.ComputeRequest
	deleted   []manager.ComputeTeardown
	ensureErr error
}

func (f *fakeComputeBackend) EnsureCompute(_ context.Context, req manager.ComputeRequest) (manager.ComputeResult, error) {
	if f.ensureErr != nil {
		return manager.ComputeResult{}, f.ensureErr
	}
	f.ensured = append(f.ensured, req)
	return manager.ComputeResult{Namespace: "ns-" + req.UID, VethHost: "vh-" + req.UID, Rootfs: "/rootfs/" + req.UID}, nil
}

func (f *fakeComputeBackend) DeleteCompute(_ context.Context, td manager.ComputeTeardown) error {
	f.deleted = append(f.deleted, td)
	return nil
}

// computeEnv holds the registries a compute reconciler depends on, pre-seeded
// with a ready VPC (bridge) and a ready subnet (gateway).
type computeEnv struct {
	vpcs     *manager.VPCRegistry
	subnets  *manager.SubnetRegistry
	disks    *manager.DiskRegistry
	sgs      *manager.SecurityGroupRegistry
	computes *manager.ComputeRegistry
	store    state.Store
}

func newComputeEnv(t *testing.T) *computeEnv {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	env := &computeEnv{
		vpcs:     registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC),
		subnets:  registry.New[resource.SubnetSpec, resource.SubnetStatus](store, resource.KindSubnet),
		disks:    registry.New[resource.DiskSpec, resource.DiskStatus](store, resource.KindDisk),
		sgs:      registry.New[resource.SecurityGroupSpec, resource.SecurityGroupStatus](store, resource.KindSecurityGroup),
		computes: registry.New[resource.ComputeSpec, resource.ComputeStatus](store, resource.KindCompute),
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

func (env *computeEnv) reconciler(be manager.ComputeBackend) *manager.ComputeReconciler {
	return manager.NewComputeReconciler(env.computes, env.subnets, env.vpcs, env.disks, env.sgs, be, "")
}

func (env *computeEnv) putCompute(t *testing.T, c *resource.Compute) {
	t.Helper()
	if err := env.computes.Put(c); err != nil {
		t.Fatalf("seed compute: %v", err)
	}
}

func basicCompute(uid string) *resource.Compute {
	return &resource.Compute{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.ComputeSpec{SubnetID: "sn-1", Image: "alpine:latest"},
	}
}

func TestComputeReconcileSuccess(t *testing.T) {
	t.Parallel()

	env := newComputeEnv(t)
	d := &resource.Disk{Metadata: resource.ObjectMeta{UID: "d-1", Generation: 1}, Spec: resource.DiskSpec{SizeMB: 1024}}
	d.Status.Path = "/dev/mapper/infra-d1"
	if err := env.disks.Put(d); err != nil {
		t.Fatalf("seed disk: %v", err)
	}
	sg := &resource.SecurityGroup{Metadata: resource.ObjectMeta{UID: "sg-1", Generation: 1}, Spec: resource.SecurityGroupSpec{VPCID: "vpc-1"}}
	sg.Status.Chain = "INFRA-SG-AB"
	if err := env.sgs.Put(sg); err != nil {
		t.Fatalf("seed sg: %v", err)
	}
	c := basicCompute("i-1")
	c.Spec.SecurityGroupID = "sg-1"
	c.Spec.Disks = []resource.ComputeDiskRef{{DiskID: "d-1", MountPath: "/data"}}
	c.Spec.Ports = []string{"8080:80"}
	env.putCompute(t, c)

	be := &fakeComputeBackend{}
	rec := env.reconciler(be)
	if err := rec.Reconcile(context.Background(), "i-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got, _ := env.computes.Get("i-1")
	if !got.Status.IsReady() || !got.Status.Ready {
		t.Fatalf("status not ready: %+v", got.Status)
	}
	if got.Status.IP != "10.0.1.10" {
		t.Fatalf("ip = %q, want 10.0.1.10", got.Status.IP)
	}
	if got.Status.Namespace != "ns-i-1" || got.Status.VethHost != "vh-i-1" {
		t.Fatalf("topology not recorded: %+v", got.Status)
	}
	if !got.Metadata.HasFinalizer(resource.ComputeFinalizer) {
		t.Fatal("finalizer should be attached")
	}

	if len(be.ensured) != 1 {
		t.Fatalf("ensure called %d times, want 1", len(be.ensured))
	}
	req := be.ensured[0]
	if req.Bridge != "br-vpc1" || req.Gateway != "10.0.1.1" || req.DNS != "10.0.0.1" || req.Prefix != 24 {
		t.Fatalf("unexpected resolved request: %+v", req)
	}
	if req.SGChain != "INFRA-SG-AB" {
		t.Fatalf("sg chain = %q", req.SGChain)
	}
	if len(req.Disks) != 1 || req.Disks[0].Source != "/dev/mapper/infra-d1" || req.Disks[0].Target != "/data" {
		t.Fatalf("disk mount not resolved: %+v", req.Disks)
	}
}

func TestComputeNodeScoping(t *testing.T) {
	t.Parallel()

	env := newComputeEnv(t)
	mine := basicCompute("i-mine")
	mine.Status.NodeName = "node-a"
	env.putCompute(t, mine)
	other := basicCompute("i-other")
	other.Status.NodeName = "node-b"
	env.putCompute(t, other)

	be := &fakeComputeBackend{}
	rec := manager.NewComputeReconciler(env.computes, env.subnets, env.vpcs, env.disks, env.sgs, be, "node-a")
	for _, uid := range []string{"i-mine", "i-other"} {
		if err := rec.Reconcile(context.Background(), uid); err != nil {
			t.Fatalf("reconcile %s: %v", uid, err)
		}
	}
	if len(be.ensured) != 1 || be.ensured[0].UID != "i-mine" {
		t.Fatalf("only compute scheduled to node-a should be realized: %+v", be.ensured)
	}
	if got, _ := env.computes.Get("i-other"); got.Status.Ready {
		t.Fatal("compute on another node should be left alone")
	}
}

func TestComputeAllocatesDistinctIPs(t *testing.T) {
	t.Parallel()

	env := newComputeEnv(t)
	env.putCompute(t, basicCompute("i-1"))
	env.putCompute(t, basicCompute("i-2"))
	be := &fakeComputeBackend{}
	rec := env.reconciler(be)
	for _, uid := range []string{"i-1", "i-2"} {
		if err := rec.Reconcile(context.Background(), uid); err != nil {
			t.Fatalf("reconcile %s: %v", uid, err)
		}
	}
	a, _ := env.computes.Get("i-1")
	b, _ := env.computes.Get("i-2")
	if a.Status.IP == "" || b.Status.IP == "" || a.Status.IP == b.Status.IP {
		t.Fatalf("expected distinct IPs, got %q and %q", a.Status.IP, b.Status.IP)
	}
}

func TestComputePendingDependencies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		setup func(env *computeEnv)
	}{
		{"subnet gateway missing", func(env *computeEnv) {
			sn, _ := env.subnets.Get("sn-1")
			sn.Status.Gateway = ""
			_ = env.subnets.Put(sn)
		}},
		{"vpc bridge missing", func(env *computeEnv) {
			v, _ := env.vpcs.Get("vpc-1")
			v.Status.BridgeName = ""
			_ = env.vpcs.Put(v)
		}},
		{"disk not provisioned", func(env *computeEnv) {
			d := &resource.Disk{Metadata: resource.ObjectMeta{UID: "d-1", Generation: 1}, Spec: resource.DiskSpec{SizeMB: 1}}
			_ = env.disks.Put(d) // Status.Path empty
			c, _ := env.computes.Get("i-1")
			c.Spec.Disks = []resource.ComputeDiskRef{{DiskID: "d-1", MountPath: "/data"}}
			_ = env.computes.Put(c)
		}},
		{"security group chain missing", func(env *computeEnv) {
			sg := &resource.SecurityGroup{Metadata: resource.ObjectMeta{UID: "sg-1", Generation: 1}, Spec: resource.SecurityGroupSpec{VPCID: "vpc-1"}}
			_ = env.sgs.Put(sg) // Status.Chain empty
			c, _ := env.computes.Get("i-1")
			c.Spec.SecurityGroupID = "sg-1"
			_ = env.computes.Put(c)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := newComputeEnv(t)
			env.putCompute(t, basicCompute("i-1"))
			tc.setup(env)
			be := &fakeComputeBackend{}
			rec := env.reconciler(be)
			if err := rec.Reconcile(context.Background(), "i-1"); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			got, _ := env.computes.Get("i-1")
			if got.Status.Phase != resource.PhasePending {
				t.Fatalf("phase = %q, want Pending", got.Status.Phase)
			}
			if len(be.ensured) != 0 {
				t.Fatalf("backend should not be called while pending: %+v", be.ensured)
			}
		})
	}
}

func TestComputeFinalize(t *testing.T) {
	t.Parallel()

	env := newComputeEnv(t)
	c := basicCompute("i-1")
	c.Spec.Ports = []string{"8080:80"}
	env.putCompute(t, c)
	be := &fakeComputeBackend{}
	rec := env.reconciler(be)
	if err := rec.Reconcile(context.Background(), "i-1"); err != nil {
		t.Fatalf("reconcile up: %v", err)
	}

	cur, _ := env.computes.Get("i-1")
	now := time.Now()
	cur.Metadata.DeletionTimestamp = &now
	if err := env.computes.Put(cur); err != nil {
		t.Fatalf("mark deleting: %v", err)
	}
	if err := rec.Reconcile(context.Background(), "i-1"); err != nil {
		t.Fatalf("reconcile delete: %v", err)
	}
	if _, err := env.computes.Get("i-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("record should be removed, got %v", err)
	}
	if len(be.deleted) != 1 {
		t.Fatalf("delete called %d times, want 1", len(be.deleted))
	}
	if be.deleted[0].IP != cur.Status.IP || len(be.deleted[0].Ports) != 1 {
		t.Fatalf("teardown not populated: %+v", be.deleted[0])
	}
}

func TestComputeSubnetNotFound(t *testing.T) {
	t.Parallel()

	env := newComputeEnv(t)
	c := &resource.Compute{Metadata: resource.ObjectMeta{UID: "i-1", Generation: 1}, Spec: resource.ComputeSpec{SubnetID: "missing", Image: "alpine"}}
	env.putCompute(t, c)
	rec := env.reconciler(&fakeComputeBackend{})
	if err := rec.Reconcile(context.Background(), "i-1"); err == nil {
		t.Fatal("expected an error when the subnet is missing")
	}
	got, _ := env.computes.Get("i-1")
	if got.Status.Phase != resource.PhaseError {
		t.Fatalf("phase = %q, want Error", got.Status.Phase)
	}
}

func TestExecComputeBackendNetworkSetup(t *testing.T) {
	t.Parallel()

	rec := &fwRecorder{}
	dir := t.TempDir()
	be := manager.NewExecComputeBackendWithRunner(dir, filepath.Join(dir, "netns"), filepath.Join(dir, "cgroup"), rec.run)
	req := manager.ComputeRequest{
		UID:      "i-1",
		Bridge:   "br-vpc1",
		IP:       "10.0.1.10",
		Prefix:   24,
		Gateway:  "10.0.1.1",
		DNS:      "10.0.0.1",
		SGChain:  "INFRA-SG-AB",
		Ports:    []string{"8080:80"},
		CPU:      1,
		MemoryMB: 256,
		PidsMax:  100,
		Disks:    []manager.ComputeMount{{Source: "/dev/mapper/infra-d1", Target: filepath.Join(dir, "data")}},
	}
	res, err := be.EnsureCompute(context.Background(), req)
	if err != nil {
		t.Fatalf("ensure compute: %v", err)
	}
	if res.Namespace == "" || res.VethHost == "" {
		t.Fatalf("topology not returned: %+v", res)
	}
	if !anyCallHas(rec.calls, "netns") || !anyCallHas(rec.calls, "veth") {
		t.Fatalf("namespace/veth not created: %v", rec.calls)
	}
	if !anyCallHas(rec.calls, "br-vpc1") {
		t.Fatalf("veth not attached to bridge: %v", rec.calls)
	}
	if !anyCallHas(rec.calls, "INFRA-SG-AB") {
		t.Fatalf("security group not attached: %v", rec.calls)
	}
	if !anyCallHas(rec.calls, "DNAT") {
		t.Fatalf("port mapping not applied: %v", rec.calls)
	}
	if !anyCallHas(rec.calls, "mount") {
		t.Fatalf("disk not mounted: %v", rec.calls)
	}
}

func TestExecComputeBackendDelete(t *testing.T) {
	t.Parallel()

	rec := &fwRecorder{}
	dir := t.TempDir()
	be := manager.NewExecComputeBackendWithRunner(dir, filepath.Join(dir, "netns"), filepath.Join(dir, "cgroup"), rec.run)
	td := manager.ComputeTeardown{UID: "i-1", IP: "10.0.1.10", SGChain: "INFRA-SG-AB", Ports: []string{"8080:80"}}
	if err := be.DeleteCompute(context.Background(), td); err != nil {
		t.Fatalf("delete compute: %v", err)
	}
	if !anyCallHas(rec.calls, "del") {
		t.Fatalf("expected link/netns deletion: %v", rec.calls)
	}
	if !anyCallHas(rec.calls, "-D") {
		t.Fatalf("expected iptables rule removal: %v", rec.calls)
	}
}

// Another agent is mid-allocation: it has reserved 10.0.1.10 but not yet
// written the instance's status, so nothing lists the address as used.
func TestComputeSkipsAnAddressReservedByAnotherAgent(t *testing.T) {
	t.Parallel()

	env := newComputeEnv(t)
	if ok, err := env.store.CompareAndSwap("ipam/sn-1/10.0.1.10", nil, []byte("compute-on-another-host")); err != nil || !ok {
		t.Fatalf("seed reservation: %v %v", ok, err)
	}
	env.putCompute(t, basicCompute("i-1"))
	if err := env.reconciler(&fakeComputeBackend{}).WithAddressStore(env.store).Reconcile(context.Background(), "i-1"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _ := env.computes.Get("i-1")
	if got.Status.IP == "" || got.Status.IP == "10.0.1.10" {
		t.Fatalf("IP = %q, want an address other than the reserved 10.0.1.10", got.Status.IP)
	}
}

func TestComputeRefusesAnAddressAnotherInstanceHolds(t *testing.T) {
	t.Parallel()

	env := newComputeEnv(t)
	if ok, err := env.store.CompareAndSwap("ipam/sn-1/10.0.1.10", nil, []byte("i-other")); err != nil || !ok {
		t.Fatalf("seed reservation: %v %v", ok, err)
	}
	c := basicCompute("i-1")
	c.Status.IP = "10.0.1.10" // given before reservations, and lost the race
	env.putCompute(t, c)
	if err := env.reconciler(&fakeComputeBackend{}).WithAddressStore(env.store).Reconcile(context.Background(), "i-1"); err == nil {
		t.Fatal("expected an address conflict")
	}
	got, _ := env.computes.Get("i-1")
	named := false
	for _, cond := range got.Status.Conditions {
		named = named || strings.Contains(cond.Message, "reserved by i-other")
	}
	if got.Status.Phase != resource.PhaseError || !named {
		t.Fatalf("status %+v, want Error naming the holder", got.Status)
	}
}

func TestComputeKeepsItsReservationAcrossAFailureAndReleasesItOnDelete(t *testing.T) {
	t.Parallel()

	env := newComputeEnv(t)
	env.putCompute(t, basicCompute("i-1"))
	failing := &fakeComputeBackend{ensureErr: errors.New("image pull failed")}
	if err := env.reconciler(failing).WithAddressStore(env.store).Reconcile(context.Background(), "i-1"); err == nil {
		t.Fatal("expected the failure to surface")
	}
	first, _ := env.computes.Get("i-1")
	if err := env.reconciler(&fakeComputeBackend{}).WithAddressStore(env.store).Reconcile(context.Background(), "i-1"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	second, _ := env.computes.Get("i-1")
	if first.Status.IP == "" || second.Status.IP != first.Status.IP {
		t.Fatalf("retry moved from %q to %q", first.Status.IP, second.Status.IP)
	}

	now := time.Now()
	second.Metadata.DeletionTimestamp = &now
	if err := env.computes.Put(second); err != nil {
		t.Fatal(err)
	}
	if err := env.reconciler(&fakeComputeBackend{}).WithAddressStore(env.store).Reconcile(context.Background(), "i-1"); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if _, err := env.store.Get("ipam/sn-1/" + second.Status.IP); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("reservation of %s kept after delete: %v", second.Status.IP, err)
	}
}

func TestExecComputeBackendUndoesAFailedCreation(t *testing.T) {
	t.Parallel()

	rec := &fwRecorder{}
	// The disk's device is missing, as when its LUKS container failed to open.
	run := func(ctx context.Context, name string, args ...string) (string, error) {
		if name == "mount" {
			rec.calls = append(rec.calls, append([]string{name}, args...))
			return "special device does not exist", errors.New("exit status 32")
		}
		return rec.run(ctx, name, args...)
	}
	dir := t.TempDir()
	be := manager.NewExecComputeBackendWithRunner(dir, filepath.Join(dir, "netns"), filepath.Join(dir, "cgroup"), run)
	req := manager.ComputeRequest{
		UID: "i-1", Bridge: "br-vpc1", IP: "10.0.1.10", Prefix: 24, Gateway: "10.0.1.1",
		Disks: []manager.ComputeMount{{Source: "/dev/mapper/infra-d1", Target: filepath.Join(dir, "data")}},
	}
	if _, err := be.EnsureCompute(context.Background(), req); err == nil {
		t.Fatal("a disk that cannot be mounted must fail the creation")
	}
	// What the creation made is gone, so the next pass starts again rather
	// than finding the namespace and taking the instance as running.
	var deleted bool
	for _, c := range rec.calls {
		deleted = deleted || strings.HasPrefix(strings.Join(c, " "), "ip netns del ")
	}
	if !deleted {
		t.Fatalf("the namespace was left behind: %v", rec.calls)
	}
}

func TestUnscheduledDeletingComputeIsFinalizedByScopedAgent(t *testing.T) {
	env := newComputeEnv(t)
	backend := &fakeComputeBackend{}
	cp := basicCompute("pending-function")
	cp.Metadata.AddFinalizer(resource.ComputeFinalizer)
	now := time.Now()
	cp.Metadata.DeletionTimestamp = &now
	env.putCompute(t, cp)
	reconciler := manager.NewComputeReconciler(env.computes, env.subnets, env.vpcs, env.disks, env.sgs, backend, "node-1")
	if err := reconciler.Reconcile(context.Background(), cp.Metadata.UID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.computes.Get(cp.Metadata.UID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unscheduled deletion stuck: %v", err)
	}
	if len(backend.ensured) != 0 {
		t.Fatal("deleted pending compute was started")
	}
}

func TestLegacyComputeReceivesFiniteRuntimeLimits(t *testing.T) {
	env := newComputeEnv(t)
	cp := basicCompute("legacy-zero-limits")
	env.putCompute(t, cp)
	be := &fakeComputeBackend{}
	if err := env.reconciler(be).Reconcile(context.Background(), cp.Metadata.UID); err != nil {
		t.Fatal(err)
	}
	if len(be.ensured) != 1 {
		t.Fatal("compute was not realized")
	}
	req := be.ensured[0]
	if req.CPU != 1 || req.MemoryMB != 256 || req.PidsMax != 256 {
		t.Fatalf("unbounded legacy compute: %+v", req)
	}
}
