// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
)

// TestExecWAFChain builds a real WAF chain attached to a destination match and
// tears it down. It needs root and iptables and skips otherwise.
func TestExecWAFChain(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	if _, err := exec.LookPath("iptables"); err != nil {
		t.Skip("iptables not available")
	}

	ctx := context.Background()
	be := manager.NewExecWAF()
	const chain = "INFRA-WAF-ITEST"
	match := []string{"-d", "10.255.255.254"}
	t.Cleanup(func() { _ = be.DeleteChain(ctx, chain, match) })

	rules := []resource.WAFRuleSpec{
		{MatchType: "source_ip", MatchValue: "192.0.2.0/24", Action: "block"},
	}
	if err := be.EnsureChain(ctx, chain, match, rules, false); err != nil {
		t.Fatalf("ensure chain: %v", err)
	}
	// Idempotent: a second call must not duplicate the FORWARD jump.
	if err := be.EnsureChain(ctx, chain, match, rules, false); err != nil {
		t.Fatalf("ensure chain (again): %v", err)
	}

	out, err := exec.Command("iptables", "-S", chain).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "DROP") {
		t.Fatalf("chain not built: %v %s", err, out)
	}
	jumps, _ := exec.Command("iptables", "-S", "FORWARD").CombinedOutput()
	if strings.Count(string(jumps), chain) != 1 {
		t.Fatalf("FORWARD jump not idempotent: %s", jumps)
	}

	if err := be.DeleteChain(ctx, chain, match); err != nil {
		t.Fatalf("delete chain: %v", err)
	}
	if out, _ := exec.Command("iptables", "-S", chain).CombinedOutput(); strings.Contains(string(out), chain+" ") {
		t.Fatalf("chain not removed: %s", out)
	}
}
