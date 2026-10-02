// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// SecurityGroupBackend abstracts the iptables operations a security group needs.
// The chain it builds is jumped to from FORWARD/OUTPUT by each compute the group
// is attached to; it is not linked to a built-in chain itself.
type SecurityGroupBackend interface {
	// EnsureChain (re)creates chain as an allow-list: established traffic and the
	// given rules are accepted, everything else is dropped. Idempotent.
	EnsureChain(ctx context.Context, chain string, rules []resource.SecurityGroupRuleSpec) error
	// DeleteChain flushes and removes the chain. Idempotent.
	DeleteChain(ctx context.Context, chain string) error
}

// sgChainName derives a valid iptables chain name from a security-group UID.
func sgChainName(uid string) string {
	var b strings.Builder
	for _, r := range uid {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		}
	}
	name := "INFRA-SG-" + strings.ToUpper(b.String())
	if len(name) <= maxChainName {
		return name
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(uid))
	return fmt.Sprintf("INFRA-SG-%08X", h.Sum32())
}

// ExecSecurityGroup is a SecurityGroupBackend that shells out to iptables. It
// requires root / CAP_NET_ADMIN at run time.
type ExecSecurityGroup struct {
	run Runner
}

// NewExecSecurityGroup returns an ExecSecurityGroup using the real iptables.
func NewExecSecurityGroup() *ExecSecurityGroup {
	return &ExecSecurityGroup{run: defaultRun}
}

// NewExecSecurityGroupWithRunner returns a backend driven by a custom runner,
// used in tests to assert the issued commands without touching the kernel.
func NewExecSecurityGroupWithRunner(run Runner) *ExecSecurityGroup {
	return &ExecSecurityGroup{run: run}
}

// EnsureChain rebuilds the chain from the desired rule set on every call.
func (f *ExecSecurityGroup) EnsureChain(ctx context.Context, chain string, rules []resource.SecurityGroupRuleSpec) error {
	_, _ = f.run(ctx, "iptables", "-N", chain)
	if out, err := f.run(ctx, "iptables", "-F", chain); err != nil {
		return fmt.Errorf("manager: flush sg chain %q: %w: %s", chain, err, strings.TrimSpace(out))
	}
	if out, err := f.run(ctx, "iptables", "-A", chain, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("manager: sg conntrack rule %q: %w: %s", chain, err, strings.TrimSpace(out))
	}
	for i, rule := range rules {
		args := append([]string{"-A", chain}, sgRuleArgs(rule)...)
		if out, err := f.run(ctx, "iptables", args...); err != nil {
			return fmt.Errorf("manager: apply sg rule %d to %q: %w: %s", i, chain, err, strings.TrimSpace(out))
		}
	}
	if out, err := f.run(ctx, "iptables", "-A", chain, "-j", "DROP"); err != nil {
		return fmt.Errorf("manager: sg default drop %q: %w: %s", chain, err, strings.TrimSpace(out))
	}
	return nil
}

// DeleteChain flushes and removes the chain (best effort).
func (f *ExecSecurityGroup) DeleteChain(ctx context.Context, chain string) error {
	_, _ = f.run(ctx, "iptables", "-F", chain)
	_, _ = f.run(ctx, "iptables", "-X", chain)
	return nil
}

// sgRuleArgs translates a security-group rule into iptables ACCEPT arguments.
func sgRuleArgs(rule resource.SecurityGroupRuleSpec) []string {
	cidr := rule.CIDR
	if cidr == "" {
		cidr = "0.0.0.0/0"
	}
	args := []string{"-s", cidr}
	if rule.Protocol != "all" {
		args = append(args, "-p", rule.Protocol)
	}
	if rule.Port != 0 && rule.Protocol != "icmp" && rule.Protocol != "all" {
		args = append(args, "--dport", strconv.Itoa(rule.Port))
	}
	return append(args, "-j", "ACCEPT")
}

// SecurityGroupRegistry is the typed store of security groups.
type SecurityGroupRegistry = registry.Registry[resource.SecurityGroupSpec, resource.SecurityGroupStatus]

// SecurityGroupRuleRegistry is the typed store of security-group rules.
type SecurityGroupRuleRegistry = registry.Registry[resource.SecurityGroupRuleSpec, resource.SecurityGroupRuleStatus]

// SecurityGroupReconciler realizes a security group as an iptables allow-list
// chain populated from the rules that reference it.
type SecurityGroupReconciler struct {
	reg   *SecurityGroupRegistry
	rules *SecurityGroupRuleRegistry
	fw    SecurityGroupBackend
}

// NewSecurityGroupReconciler returns a reconciler backed by reg, the rule store
// and fw.
func NewSecurityGroupReconciler(reg *SecurityGroupRegistry, rules *SecurityGroupRuleRegistry, fw SecurityGroupBackend) *SecurityGroupReconciler {
	return &SecurityGroupReconciler{reg: reg, rules: rules, fw: fw}
}

// Name identifies the reconcile pass.
func (r *SecurityGroupReconciler) Name() string { return resource.KindSecurityGroup }

// ReconcileAll reconciles every security group, collecting per-group errors.
func (r *SecurityGroupReconciler) ReconcileAll(ctx context.Context) error {
	groups, err := r.reg.WithContext(ctx).List()
	if err != nil {
		return fmt.Errorf("manager: list security groups: %w", err)
	}
	var errs []error
	for i := range groups {
		uid := groups[i].Metadata.UID
		if err := r.Reconcile(ctx, uid); err != nil {
			errs = append(errs, fmt.Errorf("security group %s: %w", uid, err))
		}
	}
	return errors.Join(errs...)
}

// Reconcile brings the security group identified by uid in line with its rules.
func (r *SecurityGroupReconciler) Reconcile(ctx context.Context, uid string) error {
	sg, err := r.reg.WithContext(ctx).Get(uid)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("manager: load security group %q: %w", uid, err)
	}
	if sg.Metadata.IsDeleting() {
		return r.finalize(ctx, sg)
	}
	return r.ensure(ctx, sg)
}

func (r *SecurityGroupReconciler) ensure(ctx context.Context, sg *resource.SecurityGroup) error {
	if !sg.Metadata.HasFinalizer(resource.SecurityGroupFinalizer) {
		sg.Metadata.AddFinalizer(resource.SecurityGroupFinalizer)
	}
	sg.Status.SetPhase(resource.PhaseReconciling, "Reconciling", "building chain")

	rules, err := r.rulesFor(sg.Metadata.UID)
	if err != nil {
		sg.Status.SetPhase(resource.PhaseError, "RuleError", err.Error())
		_ = r.reg.WithContext(ctx).Put(sg)
		return err
	}

	chain := sgChainName(sg.Metadata.UID)
	if err := r.fw.EnsureChain(ctx, chain, rules); err != nil {
		sg.Status.SetPhase(resource.PhaseError, "FirewallError", err.Error())
		_ = r.reg.WithContext(ctx).Put(sg)
		return err
	}

	sg.Status.Chain = chain
	sg.Status.MarkReconciled(sg.Metadata.Generation)
	sg.Status.SetPhase(resource.PhaseReady, "Applied", "chain built")
	if err := r.reg.WithContext(ctx).Put(sg); err != nil {
		return fmt.Errorf("manager: save security group %q: %w", sg.Metadata.UID, err)
	}
	return nil
}

func (r *SecurityGroupReconciler) finalize(ctx context.Context, sg *resource.SecurityGroup) error {
	if sg.Metadata.HasFinalizer(resource.SecurityGroupFinalizer) {
		if err := r.fw.DeleteChain(ctx, sgChainName(sg.Metadata.UID)); err != nil {
			sg.Status.SetPhase(resource.PhaseError, "FirewallError", err.Error())
			_ = r.reg.WithContext(ctx).Put(sg)
			return err
		}
		sg.Metadata.RemoveFinalizer(resource.SecurityGroupFinalizer)
		sg.Status.SetPhase(resource.PhaseDeleting, "Deleting", "chain removed")
		if err := r.reg.WithContext(ctx).Put(sg); err != nil {
			return fmt.Errorf("manager: save security group %q: %w", sg.Metadata.UID, err)
		}
	}
	if len(sg.Metadata.Finalizers) == 0 {
		if err := r.reg.WithContext(ctx).Delete(sg.Metadata.UID); err != nil {
			return fmt.Errorf("manager: delete security group %q: %w", sg.Metadata.UID, err)
		}
	}
	return nil
}

// rulesFor returns the live rules that reference the security group.
func (r *SecurityGroupReconciler) rulesFor(sgID string) ([]resource.SecurityGroupRuleSpec, error) {
	all, err := r.rules.List()
	if err != nil {
		return nil, fmt.Errorf("manager: list security-group rules: %w", err)
	}
	var out []resource.SecurityGroupRuleSpec
	for i := range all {
		if all[i].Metadata.IsDeleting() {
			continue
		}
		if all[i].Spec.SecurityGroupID == sgID {
			out = append(out, all[i].Spec)
		}
	}
	return out, nil
}
