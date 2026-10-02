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

// TestExecComputeBackendNamespace brings a real network namespace and veth pair
// up against a bridge and tears it down. It needs root and iproute2 and skips
// otherwise. No image is used, so it exercises only the kernel networking path.
func TestExecComputeBackendNamespace(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("iproute2 not available")
	}

	ctx := context.Background()
	net := manager.NewExecBackend()
	const bridge = "br-itest-c"
	if err := net.EnsureBridge(ctx, manager.Bridge{Name: bridge, CIDR: "10.123.0.0/16"}); err != nil {
		t.Fatalf("ensure bridge: %v", err)
	}
	t.Cleanup(func() { _ = net.DeleteBridge(ctx, bridge) })

	be := manager.NewExecComputeBackend(t.TempDir())
	const uid = "i-itest"
	t.Cleanup(func() { _ = be.DeleteCompute(ctx, manager.ComputeTeardown{UID: uid}) })

	res, err := be.EnsureCompute(ctx, manager.ComputeRequest{
		UID:     uid,
		Bridge:  bridge,
		IP:      "10.123.0.10",
		Prefix:  16,
		Gateway: "10.123.0.1",
		DNS:     "10.123.0.1",
	})
	if err != nil {
		t.Fatalf("ensure compute: %v", err)
	}

	out, err := exec.Command("ip", "netns", "list").CombinedOutput()
	if err != nil {
		t.Fatalf("list netns: %v", err)
	}
	if !strings.Contains(string(out), res.Namespace) {
		t.Fatalf("namespace %q not created: %s", res.Namespace, out)
	}

	if err := be.DeleteCompute(ctx, manager.ComputeTeardown{UID: uid}); err != nil {
		t.Fatalf("delete compute: %v", err)
	}
	out, _ = exec.Command("ip", "netns", "list").CombinedOutput()
	if strings.Contains(string(out), res.Namespace) {
		t.Fatalf("namespace %q not removed: %s", res.Namespace, out)
	}
}
