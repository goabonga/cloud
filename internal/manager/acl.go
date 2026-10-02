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

// ACLRegistry is the typed store the ACL reconciler reads and writes.
type ACLRegistry = registry.Registry[resource.ACLPolicySpec, resource.ACLPolicyStatus]

// ACLReconciler applies access-control policies to iptables and tears them down
// on deletion via a finalizer.
type ACLReconciler struct {
	reg *ACLRegistry
	fw  FirewallBackend
}

// NewACLReconciler returns a reconciler backed by reg and fw.
func NewACLReconciler(reg *ACLRegistry, fw FirewallBackend) *ACLReconciler {
	return &ACLReconciler{reg: reg, fw: fw}
}

// Name identifies the reconcile pass.
func (r *ACLReconciler) Name() string { return resource.KindACLPolicy }

// ReconcileAll reconciles every ACL policy, collecting per-policy errors.
func (r *ACLReconciler) ReconcileAll(ctx context.Context) error {
	policies, err := r.reg.WithContext(ctx).List()
	if err != nil {
		return fmt.Errorf("manager: list acl policies: %w", err)
	}
	var errs []error
	for i := range policies {
		uid := policies[i].Metadata.UID
		if err := r.Reconcile(ctx, uid); err != nil {
			errs = append(errs, fmt.Errorf("acl %s: %w", uid, err))
		}
	}
	return errors.Join(errs...)
}

// Reconcile brings the policy identified by uid in line with its spec.
func (r *ACLReconciler) Reconcile(ctx context.Context, uid string) error {
	pol, err := r.reg.WithContext(ctx).Get(uid)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("manager: load acl %q: %w", uid, err)
	}
	if pol.Metadata.IsDeleting() {
		return r.finalize(ctx, pol)
	}
	return r.ensure(ctx, pol)
}

func (r *ACLReconciler) ensure(ctx context.Context, pol *resource.ACLPolicy) error {
	if !pol.Metadata.HasFinalizer(resource.ACLFinalizer) {
		pol.Metadata.AddFinalizer(resource.ACLFinalizer)
	}
	pol.Status.SetPhase(resource.PhaseReconciling, "Reconciling", "applying rules")

	chain := chainName(pol.Metadata.UID)
	if err := r.fw.Apply(ctx, chain, pol.Spec.Rules); err != nil {
		pol.Status.SetPhase(resource.PhaseError, "FirewallError", err.Error())
		_ = r.reg.WithContext(ctx).Put(pol)
		return err
	}

	pol.Status.Chain = chain
	pol.Status.AppliedRules = len(pol.Spec.Rules)
	pol.Status.MarkReconciled(pol.Metadata.Generation)
	pol.Status.SetPhase(resource.PhaseReady, "Applied", "rules applied")
	if err := r.reg.WithContext(ctx).Put(pol); err != nil {
		return fmt.Errorf("manager: save acl %q: %w", pol.Metadata.UID, err)
	}
	return nil
}

func (r *ACLReconciler) finalize(ctx context.Context, pol *resource.ACLPolicy) error {
	if pol.Metadata.HasFinalizer(resource.ACLFinalizer) {
		if err := r.fw.Clear(ctx, chainName(pol.Metadata.UID)); err != nil {
			pol.Status.SetPhase(resource.PhaseError, "FirewallError", err.Error())
			_ = r.reg.WithContext(ctx).Put(pol)
			return err
		}
		pol.Metadata.RemoveFinalizer(resource.ACLFinalizer)
		pol.Status.SetPhase(resource.PhaseDeleting, "Deleting", "rules removed")
		if err := r.reg.WithContext(ctx).Put(pol); err != nil {
			return fmt.Errorf("manager: save acl %q: %w", pol.Metadata.UID, err)
		}
	}
	if len(pol.Metadata.Finalizers) == 0 {
		if err := r.reg.WithContext(ctx).Delete(pol.Metadata.UID); err != nil {
			return fmt.Errorf("manager: delete acl %q: %w", pol.Metadata.UID, err)
		}
	}
	return nil
}
