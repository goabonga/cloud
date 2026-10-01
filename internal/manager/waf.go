// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// WAFBackend abstracts the iptables operations a WAF policy needs. The chain it
// builds is attached to FORWARD matched on the protected target.
type WAFBackend interface {
	// EnsureChain (re)creates chain from rules, appends a LOG when logEnabled and
	// attaches it to FORWARD with match (e.g. ["-d", ip]). Idempotent.
	EnsureChain(ctx context.Context, chain string, match []string, rules []resource.WAFRuleSpec, logEnabled bool) error
	// DeleteChain detaches and removes the chain. Idempotent.
	DeleteChain(ctx context.Context, chain string, match []string) error
}

// wafChainName derives a valid iptables chain name from a WAF policy UID.
func wafChainName(uid string) string {
	var b strings.Builder
	for _, r := range uid {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		}
	}
	name := "INFRA-WAF-" + strings.ToUpper(b.String())
	if len(name) <= maxChainName {
		return name
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(uid))
	return fmt.Sprintf("INFRA-WAF-%08X", h.Sum32())
}

// ExecWAF is a WAFBackend that shells out to iptables. It requires root /
// CAP_NET_ADMIN at run time.
type ExecWAF struct {
	run Runner
}

// NewExecWAF returns an ExecWAF using the real iptables command.
func NewExecWAF() *ExecWAF {
	return &ExecWAF{run: defaultRun}
}

// NewExecWAFWithRunner returns a backend driven by a custom runner, used in
// tests to assert the issued commands without touching the kernel.
func NewExecWAFWithRunner(run Runner) *ExecWAF {
	return &ExecWAF{run: run}
}

// EnsureChain rebuilds the chain from the rule set and links it from FORWARD.
func (f *ExecWAF) EnsureChain(ctx context.Context, chain string, match []string, rules []resource.WAFRuleSpec, logEnabled bool) error {
	_, _ = f.run(ctx, "iptables", "-N", chain)
	if out, err := f.run(ctx, "iptables", "-F", chain); err != nil {
		return fmt.Errorf("manager: flush waf chain %q: %w: %s", chain, err, strings.TrimSpace(out))
	}
	for i, rule := range rules {
		args := append([]string{"-A", chain}, wafRuleArgs(chain, i, rule)...)
		if out, err := f.run(ctx, "iptables", args...); err != nil {
			return fmt.Errorf("manager: apply waf rule %d to %q: %w: %s", i, chain, err, strings.TrimSpace(out))
		}
	}
	if logEnabled {
		if out, err := f.run(ctx, "iptables", "-A", chain, "-j", "LOG", "--log-prefix", "INFRA-WAF: "); err != nil {
			return fmt.Errorf("manager: waf log rule %q: %w: %s", chain, err, strings.TrimSpace(out))
		}
	}
	link := append(append([]string{"FORWARD"}, match...), "-j", chain)
	f.unlinkOthers(ctx, chain, link)
	if _, err := f.run(ctx, "iptables", append([]string{"-C"}, link...)...); err != nil {
		if out, err := f.run(ctx, "iptables", append([]string{"-A"}, link...)...); err != nil {
			return fmt.Errorf("manager: link waf chain %q: %w: %s", chain, err, strings.TrimSpace(out))
		}
	}
	return nil
}

// unlinkOthers removes every FORWARD rule jumping to chain but link: one an
// agent wrote for a target since moved, or before the VPC's VRF, when the
// match named no bridge.
func (f *ExecWAF) unlinkOthers(ctx context.Context, chain string, link []string) {
	out, err := f.run(ctx, "iptables", "-S", "FORWARD")
	if err != nil {
		return
	}
	want := "-A " + strings.Join(link, " ")
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasSuffix(line, " -j "+chain) || line == want {
			continue
		}
		_, _ = f.run(ctx, "iptables", append([]string{"-D"}, strings.Fields(line)[1:]...)...)
	}
}

// DeleteChain detaches the chain from FORWARD, flushes and removes it.
func (f *ExecWAF) DeleteChain(ctx context.Context, chain string, match []string) error {
	link := append(append([]string{"-D", "FORWARD"}, match...), "-j", chain)
	_, _ = f.run(ctx, "iptables", link...)
	_, _ = f.run(ctx, "iptables", "-F", chain)
	_, _ = f.run(ctx, "iptables", "-X", chain)
	return nil
}

// wafRuleArgs maps an abstract WAF rule onto iptables match and target arguments.
func wafRuleArgs(chain string, idx int, r resource.WAFRuleSpec) []string {
	var args []string
	switch r.MatchType {
	case "source_ip", "ip":
		if r.MatchValue != "" {
			args = append(args, "-s", r.MatchValue)
		}
	case "dest_ip":
		if r.MatchValue != "" {
			args = append(args, "-d", r.MatchValue)
		}
	case "protocol":
		if r.MatchValue != "" {
			args = append(args, "-p", r.MatchValue)
		}
	case "port":
		if r.MatchValue != "" {
			args = append(args, "-p", "tcp", "--dport", r.MatchValue)
		}
	case "string", "path", "uri", "header":
		if r.MatchValue != "" {
			args = append(args, "-m", "string", "--algo", "bm", "--string", r.MatchValue)
		}
	}
	switch r.Action {
	case "block":
		args = append(args, "-j", "DROP")
	case "allow":
		args = append(args, "-j", "ACCEPT")
	case "log":
		args = append(args, "-j", "LOG", "--log-prefix", "INFRA-WAF: ")
	case "ratelimit":
		rate := r.RateLimit
		if rate <= 0 {
			rate = 100
		}
		window := r.RateWindow
		if window <= 0 {
			window = 60
		}
		perSec := rate / window
		if perSec <= 0 {
			perSec = 1
		}
		args = append(args,
			"-m", "hashlimit",
			"--hashlimit-above", strconv.Itoa(perSec)+"/sec",
			"--hashlimit-mode", "srcip",
			"--hashlimit-name", fmt.Sprintf("%s-%d", strings.ToLower(chain), idx),
			"-j", "DROP",
		)
	}
	return args
}

// WAFPolicyRegistry is the typed store of WAF policies.
type WAFPolicyRegistry = registry.Registry[resource.WAFPolicySpec, resource.WAFPolicyStatus]

// WAFRuleRegistry is the typed store of WAF rules.
type WAFRuleRegistry = registry.Registry[resource.WAFRuleSpec, resource.WAFRuleStatus]

// WAFReconciler realizes a WAF policy as an iptables chain populated from its
// rules and attached to the protected target (compute, subnet or igw).
type WAFReconciler struct {
	reg      *WAFPolicyRegistry
	rules    *WAFRuleRegistry
	computes *ComputeRegistry
	subnets  *SubnetRegistry
	igws     *IGWRegistry
	vpcs     *VPCRegistry
	backend  WAFBackend
}

// NewWAFReconciler returns a reconciler backed by the policy and rule stores, the
// target stores and the backend.
func NewWAFReconciler(reg *WAFPolicyRegistry, rules *WAFRuleRegistry, computes *ComputeRegistry, subnets *SubnetRegistry, igws *IGWRegistry, vpcs *VPCRegistry, backend WAFBackend) *WAFReconciler {
	return &WAFReconciler{reg: reg, rules: rules, computes: computes, subnets: subnets, igws: igws, vpcs: vpcs, backend: backend}
}

// Name identifies the reconcile pass.
func (r *WAFReconciler) Name() string { return resource.KindWAFPolicy }

// ReconcileAll reconciles every WAF policy, collecting per-policy errors.
func (r *WAFReconciler) ReconcileAll(ctx context.Context) error {
	policies, err := r.reg.List()
	if err != nil {
		return fmt.Errorf("manager: list waf policies: %w", err)
	}
	var errs []error
	for i := range policies {
		uid := policies[i].Metadata.UID
		if err := r.Reconcile(ctx, uid); err != nil {
			errs = append(errs, fmt.Errorf("waf %s: %w", uid, err))
		}
	}
	return errors.Join(errs...)
}

// Reconcile brings the WAF policy identified by uid in line with its rules.
func (r *WAFReconciler) Reconcile(ctx context.Context, uid string) error {
	pol, err := r.reg.Get(uid)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("manager: load waf %q: %w", uid, err)
	}
	if pol.Metadata.IsDeleting() {
		return r.finalize(ctx, pol)
	}
	return r.ensure(ctx, pol)
}

func (r *WAFReconciler) ensure(ctx context.Context, pol *resource.WAFPolicy) error {
	if !pol.Metadata.HasFinalizer(resource.WAFPolicyFinalizer) {
		pol.Metadata.AddFinalizer(resource.WAFPolicyFinalizer)
	}

	match, ready, err := r.targetMatch(pol.Spec)
	if err != nil {
		pol.Status.SetPhase(resource.PhaseError, "TargetError", err.Error())
		_ = r.reg.Put(pol)
		return err
	}
	if !ready {
		pol.Status.SetPhase(resource.PhasePending, "WaitingForTarget", "target not ready")
		return r.reg.Put(pol)
	}

	rules, err := r.rulesFor(pol.Metadata.UID)
	if err != nil {
		pol.Status.SetPhase(resource.PhaseError, "RuleError", err.Error())
		_ = r.reg.Put(pol)
		return err
	}

	chain := wafChainName(pol.Metadata.UID)
	pol.Status.SetPhase(resource.PhaseReconciling, "Reconciling", "building chain")
	if err := r.backend.EnsureChain(ctx, chain, match, rules, pol.Spec.LogEnabled); err != nil {
		pol.Status.SetPhase(resource.PhaseError, "FirewallError", err.Error())
		_ = r.reg.Put(pol)
		return err
	}

	pol.Status.Chain = chain
	pol.Status.MarkReconciled(pol.Metadata.Generation)
	pol.Status.SetPhase(resource.PhaseReady, "Applied", "chain attached")
	if err := r.reg.Put(pol); err != nil {
		return fmt.Errorf("manager: save waf %q: %w", pol.Metadata.UID, err)
	}
	return nil
}

func (r *WAFReconciler) finalize(ctx context.Context, pol *resource.WAFPolicy) error {
	if pol.Metadata.HasFinalizer(resource.WAFPolicyFinalizer) {
		match, _, _ := r.targetMatch(pol.Spec)
		if err := r.backend.DeleteChain(ctx, wafChainName(pol.Metadata.UID), match); err != nil {
			pol.Status.SetPhase(resource.PhaseError, "FirewallError", err.Error())
			_ = r.reg.Put(pol)
			return err
		}
		pol.Metadata.RemoveFinalizer(resource.WAFPolicyFinalizer)
		pol.Status.SetPhase(resource.PhaseDeleting, "Deleting", "chain removed")
		if err := r.reg.Put(pol); err != nil {
			return fmt.Errorf("manager: save waf %q: %w", pol.Metadata.UID, err)
		}
	}
	if len(pol.Metadata.Finalizers) == 0 {
		if err := r.reg.Delete(pol.Metadata.UID); err != nil {
			return fmt.Errorf("manager: delete waf %q: %w", pol.Metadata.UID, err)
		}
	}
	return nil
}

// targetMatch resolves the iptables match selecting inbound traffic to the
// policy's target. ready is false when the target is not provisioned yet.
func (r *WAFReconciler) targetMatch(spec resource.WAFPolicySpec) (match []string, ready bool, err error) {
	switch spec.TargetType {
	case "compute":
		c, cErr := r.computes.Get(spec.TargetID)
		if errors.Is(cErr, state.ErrNotFound) {
			return nil, false, fmt.Errorf("compute %q not found", spec.TargetID)
		}
		if cErr != nil {
			return nil, false, cErr
		}
		if c.Status.IP == "" {
			return nil, false, nil
		}
		sn, sErr := r.subnets.Get(c.Spec.SubnetID)
		if sErr != nil {
			return nil, false, sErr
		}
		bridge, bErr := r.bridgeOf(sn.Spec.VPCID)
		if bErr != nil || bridge == "" {
			return nil, false, bErr
		}
		return []string{"-o", bridge, "-d", c.Status.IP}, true, nil
	case "subnet":
		s, sErr := r.subnets.Get(spec.TargetID)
		if errors.Is(sErr, state.ErrNotFound) {
			return nil, false, fmt.Errorf("subnet %q not found", spec.TargetID)
		}
		if sErr != nil {
			return nil, false, sErr
		}
		bridge, bErr := r.bridgeOf(s.Spec.VPCID)
		if bErr != nil || bridge == "" {
			return nil, false, bErr
		}
		return []string{"-o", bridge, "-d", s.Spec.CIDR}, true, nil
	case "igw":
		igw, iErr := r.igws.Get(spec.TargetID)
		if errors.Is(iErr, state.ErrNotFound) {
			return nil, false, fmt.Errorf("igw %q not found", spec.TargetID)
		}
		if iErr != nil {
			return nil, false, iErr
		}
		vpc, vErr := r.vpcs.Get(igw.Spec.VPCID)
		if vErr != nil {
			return nil, false, vErr
		}
		if vpc.Status.BridgeName == "" {
			return nil, false, nil
		}
		return []string{"-i", vpc.Status.BridgeName}, true, nil
	default:
		return nil, false, fmt.Errorf("unsupported target type %q", spec.TargetType)
	}
}

// rulesFor returns the live rules of a policy ordered by priority.
func (r *WAFReconciler) rulesFor(policyID string) ([]resource.WAFRuleSpec, error) {
	all, err := r.rules.List()
	if err != nil {
		return nil, fmt.Errorf("manager: list waf rules: %w", err)
	}
	var out []resource.WAFRuleSpec
	for i := range all {
		if all[i].Metadata.IsDeleting() {
			continue
		}
		if all[i].Spec.PolicyID == policyID {
			out = append(out, all[i].Spec)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out, nil
}

// bridgeOf returns the bridge of a VPC, empty while it is not provisioned. A
// target is matched on its VPC's bridge as well as its addresses: two VPCs may
// number their instances alike.
func (r *WAFReconciler) bridgeOf(vpcID string) (string, error) {
	vpc, err := r.vpcs.Get(vpcID)
	if err != nil {
		return "", err
	}
	return vpc.Status.BridgeName, nil
}
