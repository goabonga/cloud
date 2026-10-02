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

	"github.com/goabonga/infrastructure/internal/manager"
)

// TestExecPeeringLink links two real bridges with a veth pair and tears it down.
// It needs root and iproute2 and skips otherwise.
func TestExecPeeringLink(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("iproute2 not available")
	}

	ctx := context.Background()
	net := manager.NewExecBackend()
	const br1, br2 = "br-itest-p1", "br-itest-p2"
	for _, b := range []string{br1, br2} {
		if err := net.EnsureBridge(ctx, manager.Bridge{Name: b}); err != nil {
			t.Fatalf("ensure bridge %s: %v", b, err)
		}
		b := b
		t.Cleanup(func() { _ = net.DeleteBridge(ctx, b) })
	}

	be := manager.NewExecPeering()
	const veth1, veth2 = "pa-itest", "pb-itest"
	t.Cleanup(func() { _ = be.DeleteLink(ctx, veth1) })

	if err := be.EnsureLink(ctx, veth1, veth2, br1, br2); err != nil {
		t.Fatalf("ensure link: %v", err)
	}
	// Idempotent: a second call is a no-op.
	if err := be.EnsureLink(ctx, veth1, veth2, br1, br2); err != nil {
		t.Fatalf("ensure link (again): %v", err)
	}

	out, err := exec.Command("ip", "link", "show", veth1).CombinedOutput()
	if err != nil || !strings.Contains(string(out), veth1) {
		t.Fatalf("veth %q not created: %v %s", veth1, err, out)
	}

	if err := be.DeleteLink(ctx, veth1); err != nil {
		t.Fatalf("delete link: %v", err)
	}
	if out, err := exec.Command("ip", "link", "show", veth1).CombinedOutput(); err == nil {
		t.Fatalf("veth %q not removed: %s", veth1, out)
	}
}
