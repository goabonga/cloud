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

// TestExecLBService creates a real IPVS virtual service with one real server in
// the load-balancer namespace of a VPC plugged into a bridge, and tears it
// down. It needs root, ipvsadm and the infra-netns@ unit, and skips otherwise.
func TestExecLBService(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	if _, err := exec.LookPath("ipvsadm"); err != nil {
		t.Skip("ipvsadm not available")
	}
	if err := exec.Command("systemctl", "cat", "infra-netns@.service").Run(); err != nil {
		t.Skip("infra-netns@.service not installed")
	}

	ctx := context.Background()
	net := manager.NewExecBackend()
	const bridge, vpc = "br-itest-lb", "itest-lb"
	if err := net.EnsureBridge(ctx, manager.Bridge{Name: bridge}); err != nil {
		t.Fatalf("ensure bridge: %v", err)
	}
	t.Cleanup(func() { _ = net.DeleteBridge(ctx, bridge) })
	if err := net.EnsureNodePort(ctx, vpc, bridge); err != nil {
		t.Fatalf("ensure node port: %v", err)
	}
	t.Cleanup(func() { _ = net.DeleteNodePort(ctx, vpc) })

	be := manager.NewExecLB()
	const vip = "10.250.0.1"
	const port = 8080
	t.Cleanup(func() { _ = be.DeleteService(ctx, vpc, bridge, vip, port, "tcp") })

	servers := []manager.LBRealServer{{IP: "10.250.0.2", Port: 80, Weight: 1}}
	if err := be.EnsureService(ctx, vpc, bridge, vip, port, "tcp", "round_robin", servers); err != nil {
		t.Fatalf("ensure service: %v", err)
	}
	// Idempotent: a second call must converge without error.
	if err := be.EnsureService(ctx, vpc, bridge, vip, port, "tcp", "round_robin", servers); err != nil {
		t.Fatalf("ensure service (again): %v", err)
	}

	ipvs := func() string {
		pid, _ := exec.Command("systemctl", "show", "--property=MainPID", "--value", "infra-netns@lb-"+vpc+".service").Output()
		out, _ := exec.Command("nsenter", "--net=/proc/"+strings.TrimSpace(string(pid))+"/ns/net", "--",
			"ipvsadm", "-Ln", "-t", vip+":8080").CombinedOutput()
		return string(out)
	}
	if out := ipvs(); !strings.Contains(out, "10.250.0.2:80") {
		t.Fatalf("service/real server not present in the namespace: %s", out)
	}
	if out, _ := exec.Command("ipvsadm", "-Ln", "-t", vip+":8080").CombinedOutput(); strings.Contains(string(out), "10.250.0.2:80") {
		t.Fatalf("the service must not be in the host: %s", out)
	}

	if err := be.DeleteService(ctx, vpc, bridge, vip, port, "tcp"); err != nil {
		t.Fatalf("delete service: %v", err)
	}
	if out := ipvs(); strings.Contains(out, "10.250.0.2:80") {
		t.Fatalf("service not removed: %s", out)
	}
}
