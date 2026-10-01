// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"errors"
	"fmt"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// VPCRegistry is the typed store the VPC reconciler reads and writes.
type VPCRegistry = registry.Registry[resource.VPCSpec, resource.VPCStatus]

// VPCReconciler drives a single VPC toward its desired state by ensuring the
// backing Linux bridge exists, and tears it down on deletion via a finalizer.
type VPCReconciler struct {
	reg *VPCRegistry
	net NetworkBackend
}

// NewVPCReconciler returns a reconciler backed by reg and net.
func NewVPCReconciler(reg *VPCRegistry, net NetworkBackend) *VPCReconciler {
	return &VPCReconciler{reg: reg, net: net}
}

// Name identifies the reconcile pass.
func (r *VPCReconciler) Name() string { return resource.KindVPC }

// ReconcileAll reconciles every VPC, collecting per-VPC errors.
func (r *VPCReconciler) ReconcileAll(ctx context.Context) error {
	vpcs, err := r.reg.List()
	if err != nil {
		return fmt.Errorf("manager: list vpcs: %w", err)
	}
	var errs []error
	for i := range vpcs {
		uid := vpcs[i].Metadata.UID
		if err := r.Reconcile(ctx, uid); err != nil {
			errs = append(errs, fmt.Errorf("vpc %s: %w", uid, err))
		}
	}
	return errors.Join(errs...)
}

// Reconcile brings the VPC identified by uid in line with its spec. It is safe
// to call repeatedly: a missing resource is a no-op, an active resource ensures
// its bridge, and a deleting resource runs its finalizer.
func (r *VPCReconciler) Reconcile(ctx context.Context, uid string) error {
	vpc, err := r.reg.Get(uid)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("manager: load vpc %q: %w", uid, err)
	}

	if vpc.Metadata.IsDeleting() {
		return r.finalize(ctx, vpc)
	}
	return r.ensure(ctx, vpc)
}

func (r *VPCReconciler) ensure(ctx context.Context, vpc *resource.VPC) error {
	if !vpc.Metadata.HasFinalizer(resource.VPCFinalizer) {
		vpc.Metadata.AddFinalizer(resource.VPCFinalizer)
	}
	vpc.Status.SetPhase(resource.PhaseReconciling, "Reconciling", "ensuring bridge")

	name := bridgeName(vpc.Metadata.UID)
	if err := r.net.EnsureBridge(ctx, Bridge{Name: name, CIDR: vpc.Spec.CIDR}); err != nil {
		vpc.Status.SetPhase(resource.PhaseError, "BridgeError", err.Error())
		_ = r.reg.Put(vpc)
		return err
	}
	if err := r.net.EnsureNodePort(ctx, vpc.Metadata.UID, name); err != nil {
		vpc.Status.SetPhase(resource.PhaseError, "NodePortError", err.Error())
		_ = r.reg.Put(vpc)
		return err
	}

	vpc.Status.BridgeName = name
	vpc.Status.MarkReconciled(vpc.Metadata.Generation)
	vpc.Status.SetPhase(resource.PhaseReady, "Reconciled", "bridge ready")
	if err := r.reg.Put(vpc); err != nil {
		return fmt.Errorf("manager: save vpc %q: %w", vpc.Metadata.UID, err)
	}
	return nil
}

func (r *VPCReconciler) finalize(ctx context.Context, vpc *resource.VPC) error {
	if vpc.Metadata.HasFinalizer(resource.VPCFinalizer) {
		if err := r.net.DeleteNodePort(ctx, vpc.Metadata.UID); err != nil {
			vpc.Status.SetPhase(resource.PhaseError, "NodePortError", err.Error())
			_ = r.reg.Put(vpc)
			return err
		}
		if err := r.net.DeleteBridge(ctx, bridgeName(vpc.Metadata.UID)); err != nil {
			vpc.Status.SetPhase(resource.PhaseError, "BridgeError", err.Error())
			_ = r.reg.Put(vpc)
			return err
		}
		vpc.Metadata.RemoveFinalizer(resource.VPCFinalizer)
		vpc.Status.SetPhase(resource.PhaseDeleting, "Deleting", "bridge removed")
		if err := r.reg.Put(vpc); err != nil {
			return fmt.Errorf("manager: save vpc %q: %w", vpc.Metadata.UID, err)
		}
	}
	// Once no finalizers remain, the record can leave the store.
	if len(vpc.Metadata.Finalizers) == 0 {
		if err := r.reg.Delete(vpc.Metadata.UID); err != nil {
			return fmt.Errorf("manager: delete vpc %q: %w", vpc.Metadata.UID, err)
		}
	}
	return nil
}
