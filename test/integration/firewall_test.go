// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
)

// TestExecFirewallRealChain applies and tears down a real iptables chain. It
// needs root / CAP_NET_ADMIN and the `iptables` command, and skips otherwise.
func TestExecFirewallRealChain(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root / CAP_NET_ADMIN")
	}
	if _, err := exec.LookPath("iptables"); err != nil {
		t.Skip("iptables not available")
	}

	ctx := context.Background()
	fw := manager.NewExecFirewall()
	const chain = "INFRA-ACL-ITEST"
	t.Cleanup(func() { _ = fw.Clear(ctx, chain) })

	rules := []resource.ACLRule{
		{Action: "allow", Protocol: "tcp", Port: 65000, RateLimit: "5/second"},
		{Action: "deny"},
	}
	if err := fw.Apply(ctx, chain, rules); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := fw.Clear(ctx, chain); err != nil {
		t.Fatalf("clear: %v", err)
	}
}
