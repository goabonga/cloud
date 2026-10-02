// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

// maxChainName is the iptables chain name length limit.
const maxChainName = 28

// FirewallBackend abstracts the iptables operations the agent needs to apply an
// access-control policy as an ingress chain.
type FirewallBackend interface {
	// Apply (re)creates chain with rules and ensures INPUT jumps to it.
	Apply(ctx context.Context, chain string, rules []resource.ACLRule) error
	// Clear removes the chain and its INPUT jump. Idempotent.
	Clear(ctx context.Context, chain string) error
}

// chainName derives a valid iptables chain name from a policy UID.
func chainName(uid string) string {
	var b strings.Builder
	for _, r := range uid {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		}
	}
	name := "INFRA-ACL-" + strings.ToUpper(b.String())
	if len(name) <= maxChainName {
		return name
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(uid))
	return fmt.Sprintf("INFRA-ACL-%08X", h.Sum32())
}

// ExecFirewall is a FirewallBackend that shells out to iptables. It requires
// root / CAP_NET_ADMIN at run time.
type ExecFirewall struct {
	run Runner
}

// NewExecFirewall returns an ExecFirewall using the real iptables command.
func NewExecFirewall() *ExecFirewall {
	return &ExecFirewall{run: defaultRun}
}

// NewExecFirewallWithRunner returns an ExecFirewall driven by a custom runner,
// used in tests to assert the issued commands without touching the kernel.
func NewExecFirewallWithRunner(run Runner) *ExecFirewall {
	return &ExecFirewall{run: run}
}

// Apply (re)creates the chain, populates it, and links it from INPUT.
func (f *ExecFirewall) Apply(ctx context.Context, chain string, rules []resource.ACLRule) error {
	// Create the chain if absent (ignore "already exists"), then flush it.
	_, _ = f.run(ctx, "iptables", "-N", chain)
	if out, err := f.run(ctx, "iptables", "-F", chain); err != nil {
		return fmt.Errorf("manager: flush chain %q: %w: %s", chain, err, strings.TrimSpace(out))
	}
	for i, rule := range rules {
		args := append([]string{"-A", chain}, ruleArgs(rule)...)
		if out, err := f.run(ctx, "iptables", args...); err != nil {
			return fmt.Errorf("manager: apply rule %d to %q: %w: %s", i, chain, err, strings.TrimSpace(out))
		}
	}
	// Ensure INPUT jumps to the chain exactly once.
	if _, err := f.run(ctx, "iptables", "-C", "INPUT", "-j", chain); err != nil {
		if out, err := f.run(ctx, "iptables", "-A", "INPUT", "-j", chain); err != nil {
			return fmt.Errorf("manager: link chain %q: %w: %s", chain, err, strings.TrimSpace(out))
		}
	}
	return nil
}

// Clear unlinks and deletes the chain.
func (f *ExecFirewall) Clear(ctx context.Context, chain string) error {
	_, _ = f.run(ctx, "iptables", "-D", "INPUT", "-j", chain)
	_, _ = f.run(ctx, "iptables", "-F", chain)
	_, _ = f.run(ctx, "iptables", "-X", chain)
	return nil
}

// ruleArgs translates an ACL rule into iptables arguments.
func ruleArgs(rule resource.ACLRule) []string {
	var args []string
	if rule.Protocol != "" && rule.Protocol != "all" {
		args = append(args, "-p", rule.Protocol)
	}
	if rule.CIDR != "" {
		args = append(args, "-s", rule.CIDR)
	}
	if rule.Port != 0 {
		args = append(args, "--dport", strconv.Itoa(rule.Port))
	}
	if rule.RateLimit != "" {
		args = append(args, "-m", "limit", "--limit", rule.RateLimit)
	}
	target := "DROP"
	if rule.Action == "allow" {
		target = "ACCEPT"
	}
	return append(args, "-j", target)
}
