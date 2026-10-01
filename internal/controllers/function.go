// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package controllers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// functionLabel marks the compute instances a FunctionController creates, so
// they can be told apart from user-created compute without a join (purely for
// observability; the function_instance resource is the source of truth).
const functionLabel = "infra.io/function-id"

// functionInstancePortRangeLo and Hi bound the host ports FunctionController
// allocates for pool instances, kept disjoint from ports a user might
// deliberately choose for their own compute.
const (
	functionInstancePortRangeLo = 30000
	functionInstancePortRangeHi = 32767
)

// functionDeps groups the registries FunctionController consults to decide
// whether a function's shape is realizable, mirroring the checks
// ComputeReconciler.resolve performs for an actual compute (subnet gateway,
// VPC bridge, security-group chain) without realizing anything itself - the
// compute instances it creates still go through that same reconcile pass on
// the agent.
type functionDeps struct {
	subnets *registry.Registry[resource.SubnetSpec, resource.SubnetStatus]
	vpcs    *registry.Registry[resource.VPCSpec, resource.VPCStatus]
	sgs     *registry.Registry[resource.SecurityGroupSpec, resource.SecurityGroupStatus]
}

// ready reports whether spec's dependencies are resolved. A false result with
// a nil error means "not yet", a non-nil error means a referenced dependency
// is missing entirely.
func (d functionDeps) ready(spec resource.FunctionSpec) (ok bool, reason, message string, err error) {
	subnet, err := d.subnets.Get(spec.SubnetID)
	if errors.Is(err, state.ErrNotFound) {
		return false, "SubnetMissing", fmt.Sprintf("subnet %q not found", spec.SubnetID), nil
	}
	if err != nil {
		return false, "", "", err
	}
	if subnet.Status.Gateway == "" {
		return false, "WaitingForSubnet", "subnet gateway not ready", nil
	}

	vpc, err := d.vpcs.Get(subnet.Spec.VPCID)
	if errors.Is(err, state.ErrNotFound) {
		return false, "VPCMissing", fmt.Sprintf("vpc %q not found", subnet.Spec.VPCID), nil
	}
	if err != nil {
		return false, "", "", err
	}
	if vpc.Status.BridgeName == "" {
		return false, "WaitingForVPC", "vpc bridge not ready", nil
	}

	if spec.SecurityGroupID != "" {
		sg, sgErr := d.sgs.Get(spec.SecurityGroupID)
		if errors.Is(sgErr, state.ErrNotFound) {
			return false, "SecurityGroupMissing", fmt.Sprintf("security group %q not found", spec.SecurityGroupID), nil
		}
		if sgErr != nil {
			return false, "", "", sgErr
		}
		if sg.Status.Chain == "" {
			return false, "WaitingForSG", "security-group chain not ready", nil
		}
	}

	return true, "Ready", "function dependencies resolved", nil
}

// FunctionController realizes each function's warm pool: it creates and
// deletes the compute instances backing a function's pre-started slots,
// enforcing WarmPoolPolicy's MinWarm/MaxWarm/IdleTTLSeconds, and cascades
// deletion across a function's instances and their computes. Placement of the
// computes it creates is left entirely to SchedulerController, which treats
// them like any other compute - this is a cluster-level controller, run under
// the same leader lease, because pool sizing is a cluster-wide decision
// independent of which node a slot lands on.
type FunctionController struct {
	functions *registry.Registry[resource.FunctionSpec, resource.FunctionStatus]
	instances *registry.Registry[resource.FunctionInstanceSpec, resource.FunctionInstanceStatus]
	computes  *registry.Registry[resource.ComputeSpec, resource.ComputeStatus]
	deps      functionDeps
	now       Clock
	logger    *slog.Logger
}

// NewFunctionController returns a controller reading functions, function
// instances and computes from their stores, resolving dependencies against
// subnets, vpcs and security groups. A nil clock uses time.Now; tests inject
// a fake one to make idle-TTL eviction deterministic.
func NewFunctionController(
	functions *registry.Registry[resource.FunctionSpec, resource.FunctionStatus],
	instances *registry.Registry[resource.FunctionInstanceSpec, resource.FunctionInstanceStatus],
	computes *registry.Registry[resource.ComputeSpec, resource.ComputeStatus],
	subnets *registry.Registry[resource.SubnetSpec, resource.SubnetStatus],
	vpcs *registry.Registry[resource.VPCSpec, resource.VPCStatus],
	sgs *registry.Registry[resource.SecurityGroupSpec, resource.SecurityGroupStatus],
	clock Clock,
	logger *slog.Logger,
) *FunctionController {
	if clock == nil {
		clock = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &FunctionController{
		functions: functions,
		instances: instances,
		computes:  computes,
		deps:      functionDeps{subnets: subnets, vpcs: vpcs, sgs: sgs},
		now:       clock,
		logger:    logger,
	}
}

// Name identifies the controller.
func (c *FunctionController) Name() string { return "function" }

// Reconcile drives every function's warm pool to its desired size and
// finalizes instances and functions pending deletion. It is idempotent: pool
// membership is recomputed from the function instance records each pass.
func (c *FunctionController) Reconcile(ctx context.Context) error {
	fns, err := c.functions.List()
	if err != nil {
		return fmt.Errorf("controllers: list functions: %w", err)
	}
	insts, err := c.instances.List()
	if err != nil {
		return fmt.Errorf("controllers: list function instances: %w", err)
	}
	computes, err := c.computes.List()
	if err != nil {
		return fmt.Errorf("controllers: list computes: %w", err)
	}

	computesByUID := make(map[string]resource.Compute, len(computes))
	for i := range computes {
		computesByUID[computes[i].Metadata.UID] = computes[i]
	}
	byFunction := make(map[string][]resource.FunctionInstance, len(fns))
	for i := range insts {
		fid := insts[i].Spec.FunctionID
		byFunction[fid] = append(byFunction[fid], insts[i])
	}

	var errs []error

	// Finalize instances pending deletion, independent of their function's
	// own state, so a function can be deleted and recreated without waiting
	// on stragglers from the previous generation.
	for i := range insts {
		inst := &insts[i]
		if !inst.Metadata.IsDeleting() {
			continue
		}
		if err := c.finalizeInstance(inst); err != nil {
			errs = append(errs, fmt.Errorf("instance %s: %w", inst.Metadata.UID, err))
		}
	}

	known := make(map[string]bool, len(fns))
	for i := range fns {
		fn := &fns[i]
		known[fn.Metadata.UID] = true
		if fn.Metadata.IsDeleting() {
			if err := c.finalizeFunction(ctx, fn, byFunction[fn.Metadata.UID]); err != nil {
				errs = append(errs, fmt.Errorf("function %s: %w", fn.Metadata.UID, err))
			}
			continue
		}
		if err := c.ensureFunction(ctx, fn, byFunction[fn.Metadata.UID], computesByUID); err != nil {
			errs = append(errs, fmt.Errorf("function %s: %w", fn.Metadata.UID, err))
		}
	}

	// Defensive reap: instances whose function no longer exists at all (e.g.
	// a hard-deleted store entry), rather than merely being torn down.
	for i := range insts {
		inst := &insts[i]
		if inst.Metadata.IsDeleting() || known[inst.Spec.FunctionID] {
			continue
		}
		if err := c.deleteInstance(ctx, inst); err != nil {
			errs = append(errs, fmt.Errorf("orphan instance %s: %w", inst.Metadata.UID, err))
		}
	}

	return errors.Join(errs...)
}

// ensureFunction resolves fn's dependencies, fills and trims its warm pool to
// the configured policy, and records the result.
func (c *FunctionController) ensureFunction(ctx context.Context, fn *resource.Function, insts []resource.FunctionInstance, computesByUID map[string]resource.Compute) error {
	if !fn.Metadata.HasFinalizer(resource.FunctionFinalizer) {
		fn.Metadata.AddFinalizer(resource.FunctionFinalizer)
	}

	ok, reason, message, err := c.deps.ready(fn.Spec)
	if err != nil {
		fn.Status.SetPhase(resource.PhaseError, "ResolveError", err.Error())
		_ = c.functions.Put(fn)
		return err
	}
	if !ok {
		fn.Status.SetPhase(resource.PhasePending, reason, message)
		return c.functions.Put(fn)
	}

	live, err := c.syncInstances(insts, computesByUID)
	if err != nil {
		return err
	}

	// Count towards MinWarm with a pending creation count rather than
	// appending to live: a freshly created instance's compute is not Ready
	// yet, so it must not inflate WarmCount until a later pass observes it
	// through syncInstances - this loop only needs to stop creating once
	// enough slots (live or pending) exist.
	now := c.now()
	pending := len(live)
	for pending < fn.Spec.WarmPool.MinWarm {
		if _, err := c.createInstance(fn, computesByUID, now); err != nil {
			return err
		}
		pending++
	}

	live, err = c.evictIdle(ctx, live, fn.Spec.WarmPool, now)
	if err != nil {
		return err
	}

	fn.Status.WarmCount = len(live)
	fn.Status.SetPhase(resource.PhaseReady, reason, message)
	fn.Status.MarkReconciled(fn.Metadata.Generation)
	return c.functions.Put(fn)
}

// syncInstances refreshes each non-deleting instance's phase from its backing
// compute and returns the ones that are Warm and backed by a Ready compute.
func (c *FunctionController) syncInstances(insts []resource.FunctionInstance, computesByUID map[string]resource.Compute) ([]resource.FunctionInstance, error) {
	live := make([]resource.FunctionInstance, 0, len(insts))
	for i := range insts {
		inst := insts[i]
		if inst.Metadata.IsDeleting() {
			continue
		}
		cp, ok := computesByUID[inst.Spec.ComputeID]
		phase := resource.PhasePending
		nodeName := inst.Status.NodeName
		switch {
		case !ok:
			phase = resource.PhaseError
		case cp.Status.Phase == resource.PhaseReady:
			phase = resource.PhaseReady
			nodeName = cp.Status.NodeName
		case cp.Status.Phase != "":
			phase = resource.PhaseReconciling
			nodeName = cp.Status.NodeName
		}
		if inst.Status.Phase != phase || inst.Status.NodeName != nodeName {
			inst.Status.NodeName = nodeName
			inst.Status.SetPhase(phase, "Synced", "tracks backing compute")
			if err := c.instances.Put(&inst); err != nil {
				return nil, fmt.Errorf("controllers: sync function instance %s: %w", inst.Metadata.UID, err)
			}
		}
		if phase == resource.PhaseReady && inst.Status.State == resource.FunctionInstanceWarm {
			live = append(live, inst)
		}
	}
	return live, nil
}

// createInstance creates a new compute realizing one of fn's pool slots, and
// the function-instance record joining them, both freshly Warm.
// computesByUID is updated in place with the compute it creates, so a second
// call within the same ensureFunction pass sees the port as taken.
func (c *FunctionController) createInstance(fn *resource.Function, computesByUID map[string]resource.Compute, now time.Time) (*resource.FunctionInstance, error) {
	port, err := allocatePort(computesByUID)
	if err != nil {
		return nil, fmt.Errorf("controllers: allocate port for function %s: %w", fn.Metadata.UID, err)
	}

	computeUID := newUID("compute")
	cp := &resource.Compute{
		Metadata: resource.ObjectMeta{
			UID:        computeUID,
			Name:       fn.Spec.Name,
			Generation: 1,
			Labels:     map[string]string{functionLabel: fn.Metadata.UID},
		},
		Spec: resource.ComputeSpec{
			SubnetID:        fn.Spec.SubnetID,
			SecurityGroupID: fn.Spec.SecurityGroupID,
			NodePoolID:      fn.Spec.NodePoolID,
			CPU:             fn.Spec.CPU,
			MemoryMB:        fn.Spec.MemoryMB,
			PidsMax:         fn.Spec.PidsMax,
			Image:           fn.Spec.Image,
			Command:         fn.Spec.Command,
			Env:             fn.Spec.Env,
			Ports:           []string{fmt.Sprintf("%d:%d/tcp", port, fn.Spec.Port)},
		},
	}
	if err := c.computes.Put(cp); err != nil {
		return nil, fmt.Errorf("controllers: create compute for function %s: %w", fn.Metadata.UID, err)
	}
	computesByUID[computeUID] = *cp

	inst := &resource.FunctionInstance{
		Metadata: resource.ObjectMeta{UID: newUID("fninst"), Generation: 1},
		Spec:     resource.FunctionInstanceSpec{FunctionID: fn.Metadata.UID, ComputeID: computeUID},
	}
	inst.Metadata.AddFinalizer(resource.FunctionInstanceFinalizer)
	inst.Status.State = resource.FunctionInstanceWarm
	inst.Status.LastUsedAt = now.UTC().Format(time.RFC3339)
	inst.Status.Port = port
	inst.Status.SetPhase(resource.PhasePending, "Created", "pool instance created, waiting for its compute")
	if err := c.instances.Put(inst); err != nil {
		return nil, fmt.Errorf("controllers: create function instance for function %s: %w", fn.Metadata.UID, err)
	}
	c.logger.Info("created function instance", "function", fn.Metadata.UID, "instance", inst.Metadata.UID, "compute", computeUID, "port", port)
	return inst, nil
}

// allocatePort picks a host port in the function-instance range not already
// used by any compute's port mapping, so pool instances never collide with
// each other or with user-created compute.
func allocatePort(computesByUID map[string]resource.Compute) (int, error) {
	used := make(map[int]bool, len(computesByUID))
	for _, cp := range computesByUID {
		for _, mapping := range cp.Spec.Ports {
			if port, ok := hostPortOf(mapping); ok {
				used[port] = true
			}
		}
	}
	for port := functionInstancePortRangeLo; port <= functionInstancePortRangeHi; port++ {
		if !used[port] {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no free port in %d-%d", functionInstancePortRangeLo, functionInstancePortRangeHi)
}

// hostPortOf extracts the host-side port from a "host:container/proto"
// mapping, as used by ComputeSpec.Ports.
func hostPortOf(mapping string) (int, bool) {
	host, _, ok := strings.Cut(mapping, ":")
	if !ok {
		return 0, false
	}
	port, err := strconv.Atoi(host)
	if err != nil {
		return 0, false
	}
	return port, true
}

// evictIdle enforces policy's MaxWarm cap and IdleTTLSeconds eviction over
// live, oldest-idle first, and returns the instances kept. The MinWarm most
// recently used instances are the floor and are never evicted by TTL, which
// is the mechanism behind an "always warm" tier; the MaxWarm cap, when set,
// always wins even if that means dropping below MinWarm, since it is a hard
// ceiling.
func (c *FunctionController) evictIdle(ctx context.Context, live []resource.FunctionInstance, policy resource.WarmPoolPolicy, now time.Time) ([]resource.FunctionInstance, error) {
	sort.Slice(live, func(i, j int) bool { return live[i].Status.LastUsedAt < live[j].Status.LastUsedAt })

	keep := live
	if policy.MaxWarm > 0 && len(live) > policy.MaxWarm {
		over := live[:len(live)-policy.MaxWarm]
		keep = live[len(live)-policy.MaxWarm:]
		for i := range over {
			if err := c.deleteInstance(ctx, &over[i]); err != nil {
				return nil, err
			}
		}
	}

	if policy.MinWarm >= len(keep) {
		return keep, nil
	}

	// keep is sorted oldest-first; the newest MinWarm instances are the
	// floor and are kept regardless of idle time, while older ones beyond
	// that are subject to TTL eviction.
	floorStart := len(keep) - policy.MinWarm
	kept := make([]resource.FunctionInstance, 0, len(keep))
	for i, inst := range keep {
		if i >= floorStart {
			kept = append(kept, inst)
			continue
		}
		idleSince, err := time.Parse(time.RFC3339, inst.Status.LastUsedAt)
		if err != nil || now.Sub(idleSince) >= time.Duration(policy.IdleTTLSeconds)*time.Second {
			if err := c.deleteInstance(ctx, &inst); err != nil {
				return nil, err
			}
			continue
		}
		kept = append(kept, inst)
	}
	return kept, nil
}

// deleteInstance marks inst and its backing compute for deletion. The actual
// removal of the instance record happens in finalizeInstance, once the
// compute has finished tearing down.
func (c *FunctionController) deleteInstance(ctx context.Context, inst *resource.FunctionInstance) error {
	now := c.now()
	cp, err := c.computes.Get(inst.Spec.ComputeID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		// Already gone; nothing to mark.
	case err != nil:
		return fmt.Errorf("controllers: load compute %s: %w", inst.Spec.ComputeID, err)
	case !cp.Metadata.IsDeleting():
		cp.Metadata.DeletionTimestamp = &now
		if err := c.computes.Put(cp); err != nil {
			return fmt.Errorf("controllers: mark compute %s deleting: %w", inst.Spec.ComputeID, err)
		}
	}

	if inst.Metadata.IsDeleting() {
		return nil
	}
	inst.Metadata.DeletionTimestamp = &now
	c.logger.InfoContext(ctx, "evicting function instance", "instance", inst.Metadata.UID, "compute", inst.Spec.ComputeID)
	return c.instances.Put(inst)
}

// finalizeInstance removes inst's finalizer and deletes its record once the
// backing compute is confirmed gone; otherwise it waits for the next pass.
func (c *FunctionController) finalizeInstance(inst *resource.FunctionInstance) error {
	_, err := c.computes.Get(inst.Spec.ComputeID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		inst.Metadata.RemoveFinalizer(resource.FunctionInstanceFinalizer)
		if len(inst.Metadata.Finalizers) == 0 {
			return c.instances.Delete(inst.Metadata.UID)
		}
		return c.instances.Put(inst)
	case err != nil:
		return fmt.Errorf("controllers: load compute %s: %w", inst.Spec.ComputeID, err)
	default:
		return nil // still tearing down; retry next pass
	}
}

// finalizeFunction marks every instance of fn for deletion and, once none
// remain, removes fn's finalizer so its record can go.
func (c *FunctionController) finalizeFunction(ctx context.Context, fn *resource.Function, insts []resource.FunctionInstance) error {
	if len(insts) > 0 {
		for i := range insts {
			if err := c.deleteInstance(ctx, &insts[i]); err != nil {
				return err
			}
		}
		return nil // wait for instances (and their computes) to finish tearing down
	}

	fn.Metadata.RemoveFinalizer(resource.FunctionFinalizer)
	if len(fn.Metadata.Finalizers) == 0 {
		return c.functions.Delete(fn.Metadata.UID)
	}
	return c.functions.Put(fn)
}

// newUID generates a short, kind-prefixed identifier for resources the
// function controller creates itself (e.g. "compute-a1b2c3d4"). Nothing else
// in this codebase generates resource UIDs server-side: every other resource
// is created through a client-supplied UID on PUT.
func newUID(kind string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return kind + "-" + hex.EncodeToString(b[:])
}
