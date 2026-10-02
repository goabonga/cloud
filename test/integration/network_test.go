// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/goabonga/infrastructure/internal/manager"
)

// TestExecBackendRealBridge exercises the real iproute2 backend against the
// host kernel. It needs root / CAP_NET_ADMIN and the `ip` command, and skips
// cleanly when either is missing.
func TestExecBackendRealBridge(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root / CAP_NET_ADMIN")
	}
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("iproute2 (ip) not available")
	}

	ctx := context.Background()
	be := manager.NewExecBackend()
	const name = "br-itest0"
	t.Cleanup(func() { _ = be.DeleteBridge(ctx, name) })

	if err := be.EnsureBridge(ctx, manager.Bridge{Name: name, CIDR: "10.255.0.0/24"}); err != nil {
		t.Fatalf("ensure bridge: %v", err)
	}
	ok, err := be.BridgeExists(ctx, name)
	if err != nil || !ok {
		t.Fatalf("bridge should exist: ok=%v err=%v", ok, err)
	}

	if err := be.DeleteBridge(ctx, name); err != nil {
		t.Fatalf("delete bridge: %v", err)
	}
	ok, err = be.BridgeExists(ctx, name)
	if err != nil || ok {
		t.Fatalf("bridge should be gone: ok=%v err=%v", ok, err)
	}
}

// TestExecBackendRealNetworkOps exercises the subnet/igw realization ops against
// the host: a bridge gateway address and a NAT rule. Needs root; cleans up.
func TestExecBackendRealNetworkOps(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root / CAP_NET_ADMIN")
	}
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("iproute2 (ip) not available")
	}

	ctx := context.Background()
	be := manager.NewExecBackend()
	const bridge = "br-itest1"
	const addr = "10.255.1.1/24"
	const cidr = "10.255.1.0/24"

	iface, err := be.DefaultInterface(ctx)
	if err != nil {
		t.Skipf("no default interface: %v", err)
	}

	t.Cleanup(func() {
		_ = be.DeleteNAT(ctx, cidr, iface)
		_ = be.DeleteAddress(ctx, bridge, addr)
		_ = be.DeleteBridge(ctx, bridge)
	})

	if err := be.EnsureBridge(ctx, manager.Bridge{Name: bridge}); err != nil {
		t.Fatalf("ensure bridge: %v", err)
	}
	if err := be.EnsureAddress(ctx, bridge, addr); err != nil {
		t.Fatalf("ensure address: %v", err)
	}
	// Idempotent: a second call must not fail.
	if err := be.EnsureAddress(ctx, bridge, addr); err != nil {
		t.Fatalf("ensure address (repeat): %v", err)
	}
	if err := be.EnableForwarding(ctx); err != nil {
		t.Fatalf("enable forwarding: %v", err)
	}
	if err := be.EnsureNAT(ctx, cidr, iface); err != nil {
		t.Fatalf("ensure nat: %v", err)
	}
	if err := be.EnsureNAT(ctx, cidr, iface); err != nil {
		t.Fatalf("ensure nat (repeat): %v", err)
	}
}
