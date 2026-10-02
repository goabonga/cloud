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

// IGWRegistry is the typed store the IGW reconciler reads and writes.
type IGWRegistry = registry.Registry[resource.IGWSpec, resource.IGWStatus]

// IGWReconciler realizes an internet gateway by enabling IP forwarding and a
// masquerade rule for the VPC's CIDR on the host's default interface.
type IGWReconciler struct {
	reg  *IGWRegistry
	vpcs *VPCRegistry
	net  NetworkBackend
}

// NewIGWReconciler returns a reconciler backed by reg, the VPC store and net.
func NewIGWReconciler(reg *IGWRegistry, vpcs *VPCRegistry, net NetworkBackend) *IGWReconciler {
	return &IGWReconciler{reg: reg, vpcs: vpcs, net: net}
}

// Name identifies the reconcile pass.
func (r *IGWReconciler) Name() string { return resource.KindIGW }

// ReconcileAll reconciles every internet gateway, collecting per-igw errors.
func (r *IGWReconciler) ReconcileAll(ctx context.Context) error {
	igws, err := r.reg.WithContext(ctx).List()
	if err != nil {
		return fmt.Errorf("manager: list igws: %w", err)
	}
	var errs []error
	for i := range igws {
		uid := igws[i].Metadata.UID
		if err := r.Reconcile(ctx, uid); err != nil {
			errs = append(errs, fmt.Errorf("igw %s: %w", uid, err))
		}
	}
	return errors.Join(errs...)
}

// Reconcile brings the gateway identified by uid in line with its spec.
func (r *IGWReconciler) Reconcile(ctx context.Context, uid string) error {
	igw, err := r.reg.WithContext(ctx).Get(uid)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("manager: load igw %q: %w", uid, err)
	}
	if igw.Metadata.IsDeleting() {
		return r.finalize(ctx, igw)
	}
	return r.ensure(ctx, igw)
}

func (r *IGWReconciler) ensure(ctx context.Context, igw *resource.IGW) error {
	if !igw.Metadata.HasFinalizer(resource.IGWFinalizer) {
		igw.Metadata.AddFinalizer(resource.IGWFinalizer)
	}

	vpc, err := r.vpcs.Get(igw.Spec.VPCID)
	if errors.Is(err, state.ErrNotFound) {
		err = fmt.Errorf("vpc %q not found", igw.Spec.VPCID)
		igw.Status.SetPhase(resource.PhaseError, "VPCError", err.Error())
		_ = r.reg.WithContext(ctx).Put(igw)
		return err
	}
	if err != nil {
		return fmt.Errorf("manager: load vpc %q: %w", igw.Spec.VPCID, err)
	}
	if vpc.Status.BridgeName == "" {
		igw.Status.SetPhase(resource.PhasePending, "WaitingForVPC", "vpc bridge not ready")
		return r.reg.WithContext(ctx).Put(igw)
	}

	igw.Status.SetPhase(resource.PhaseReconciling, "Reconciling", "configuring NAT")
	hostIface, err := r.net.DefaultInterface(ctx)
	if err != nil {
		igw.Status.SetPhase(resource.PhaseError, "RouteError", err.Error())
		_ = r.reg.WithContext(ctx).Put(igw)
		return err
	}
	if err := r.net.EnableForwarding(ctx); err != nil {
		igw.Status.SetPhase(resource.PhaseError, "ForwardingError", err.Error())
		_ = r.reg.WithContext(ctx).Put(igw)
		return err
	}
	if err := r.net.EnsureNAT(ctx, vpc.Spec.CIDR, hostIface); err != nil {
		igw.Status.SetPhase(resource.PhaseError, "NATError", err.Error())
		_ = r.reg.WithContext(ctx).Put(igw)
		return err
	}
	// The VPC routes in its own table: without this route, its instances
	// have no way out even with the NAT in place.
	if err := r.net.EnsureEgress(ctx, igw.Spec.VPCID); err != nil {
		igw.Status.SetPhase(resource.PhaseError, "RouteError", err.Error())
		_ = r.reg.WithContext(ctx).Put(igw)
		return err
	}

	igw.Status.HostIface = hostIface
	igw.Status.Bridge = vpc.Status.BridgeName
	igw.Status.MarkReconciled(igw.Metadata.Generation)
	igw.Status.SetPhase(resource.PhaseReady, "Reconciled", "gateway ready")
	if err := r.reg.WithContext(ctx).Put(igw); err != nil {
		return fmt.Errorf("manager: save igw %q: %w", igw.Metadata.UID, err)
	}
	return nil
}

func (r *IGWReconciler) finalize(ctx context.Context, igw *resource.IGW) error {
	if igw.Metadata.HasFinalizer(resource.IGWFinalizer) {
		if err := r.net.DeleteEgress(ctx, igw.Spec.VPCID); err != nil {
			igw.Status.SetPhase(resource.PhaseError, "RouteError", err.Error())
			_ = r.reg.WithContext(ctx).Put(igw)
			return err
		}
		if vpc, err := r.vpcs.Get(igw.Spec.VPCID); err == nil && igw.Status.HostIface != "" {
			if err := r.net.DeleteNAT(ctx, vpc.Spec.CIDR, igw.Status.HostIface); err != nil {
				igw.Status.SetPhase(resource.PhaseError, "NATError", err.Error())
				_ = r.reg.WithContext(ctx).Put(igw)
				return err
			}
		}
		igw.Metadata.RemoveFinalizer(resource.IGWFinalizer)
		igw.Status.SetPhase(resource.PhaseDeleting, "Deleting", "NAT removed")
		if err := r.reg.WithContext(ctx).Put(igw); err != nil {
			return fmt.Errorf("manager: save igw %q: %w", igw.Metadata.UID, err)
		}
	}
	if len(igw.Metadata.Finalizers) == 0 {
		if err := r.reg.WithContext(ctx).Delete(igw.Metadata.UID); err != nil {
			return fmt.Errorf("manager: delete igw %q: %w", igw.Metadata.UID, err)
		}
	}
	return nil
}
