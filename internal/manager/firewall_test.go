// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"errors"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
)

// fwRecorder records iptables invocations and simulates the jump check as
// "not present" so Apply issues the INPUT jump.
type fwRecorder struct {
	calls [][]string
}

func (r *fwRecorder) run(_ context.Context, name string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	if len(args) >= 1 && args[0] == "-C" {
		return "", errors.New("rule does not exist")
	}
	return "", nil
}

func TestExecFirewallApply(t *testing.T) {
	t.Parallel()

	rec := &fwRecorder{}
	fw := manager.NewExecFirewallWithRunner(rec.run)
	const chain = "INFRA-ACL-X"
	rules := []resource.ACLRule{
		{Action: "allow", Protocol: "tcp", Port: 443, CIDR: "10.0.0.0/8"},
		{Action: "allow", Protocol: "tcp", Port: 80, RateLimit: "10/second"},
		{Action: "deny"},
	}
	if err := fw.Apply(context.Background(), chain, rules); err != nil {
		t.Fatalf("apply: %v", err)
	}

	want := [][]string{
		{"iptables", "-N", chain},
		{"iptables", "-F", chain},
		{"iptables", "-A", chain, "-p", "tcp", "-s", "10.0.0.0/8", "--dport", "443", "-j", "ACCEPT"},
		{"iptables", "-A", chain, "-p", "tcp", "--dport", "80", "-m", "limit", "--limit", "10/second", "-j", "ACCEPT"},
		{"iptables", "-A", chain, "-j", "DROP"},
		{"iptables", "-A", "INPUT", "-j", chain},
	}
	for _, w := range want {
		if !sawCall(rec.calls, w...) {
			t.Fatalf("missing call %v in %v", w, rec.calls)
		}
	}
}

func TestExecFirewallClear(t *testing.T) {
	t.Parallel()

	rec := &fwRecorder{}
	fw := manager.NewExecFirewallWithRunner(rec.run)
	const chain = "INFRA-ACL-X"
	if err := fw.Clear(context.Background(), chain); err != nil {
		t.Fatalf("clear: %v", err)
	}
	for _, w := range [][]string{
		{"iptables", "-D", "INPUT", "-j", chain},
		{"iptables", "-F", chain},
		{"iptables", "-X", chain},
	} {
		if !sawCall(rec.calls, w...) {
			t.Fatalf("missing call %v in %v", w, rec.calls)
		}
	}
}
