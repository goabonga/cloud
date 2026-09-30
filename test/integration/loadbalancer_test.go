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

// TestExecLBService creates a real IPVS virtual service with one real server on
// a VIP attached to a bridge and tears it down. It needs root and ipvsadm and
// skips otherwise.
func TestExecLBService(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	if _, err := exec.LookPath("ipvsadm"); err != nil {
		t.Skip("ipvsadm not available")
	}

	ctx := context.Background()
	net := manager.NewExecBackend()
	const bridge = "br-itest-lb"
	if err := net.EnsureBridge(ctx, manager.Bridge{Name: bridge}); err != nil {
		t.Fatalf("ensure bridge: %v", err)
	}
	t.Cleanup(func() { _ = net.DeleteBridge(ctx, bridge) })
	const nodePort = "np-itest-lb"
	if err := net.EnsureNodePort(ctx, bridge, nodePort, "nb-itest-lb"); err != nil {
		t.Fatalf("ensure node port: %v", err)
	}
	t.Cleanup(func() {
		_ = net.DeleteNodePort(ctx, nodePort)
		_ = exec.Command("iptables", "-t", "nat", "-D", "POSTROUTING", "-o", nodePort, "-m", "ipvs", "--ipvs", "-j", "MASQUERADE").Run()
	})

	be := manager.NewExecLB()
	const vip = "10.250.0.1"
	const port = 8080
	t.Cleanup(func() { _ = be.DeleteService(ctx, vip, port, "tcp", bridge) })

	servers := []manager.LBRealServer{{IP: "10.250.0.2", Port: 80, Weight: 1}}
	if err := be.EnsureService(ctx, vip, port, "tcp", "round_robin", bridge, nodePort, servers); err != nil {
		t.Fatalf("ensure service: %v", err)
	}
	// Idempotent: a second call must converge without error.
	if err := be.EnsureService(ctx, vip, port, "tcp", "round_robin", bridge, nodePort, servers); err != nil {
		t.Fatalf("ensure service (again): %v", err)
	}

	out, err := exec.Command("ipvsadm", "-Ln", "-t", vip+":8080").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "10.250.0.2:80") {
		t.Fatalf("service/real server not present: %v %s", err, out)
	}

	if err := be.DeleteService(ctx, vip, port, "tcp", bridge); err != nil {
		t.Fatalf("delete service: %v", err)
	}
	if out, _ := exec.Command("ipvsadm", "-Ln", "-t", vip+":8080").CombinedOutput(); strings.Contains(string(out), "10.250.0.2:80") {
		t.Fatalf("service not removed: %s", out)
	}
}
