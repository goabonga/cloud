// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/hypervisor"
	"github.com/goabonga/infrastructure/internal/manager"
)

// TestHypervisorBootNetworking is the real proof for this milestone: boot
// a VM with a tap-backed virtio-net device, statically configure its IP
// via the kernel's own ip= cmdline parameter (the same early,
// rootfs-independent configuration path internal/manager's guestCmdline
// uses for the existing cloud-hypervisor backend), and ping it from the
// host. A successful ping is a full round trip through both directions of
// this milestone's new code: the host's ICMP echo request crosses the
// bridge onto the tap, Net.ReadLoop delivers it into the guest's rx
// virtqueue, the guest kernel's network stack answers it, and the reply
// crosses back out through Net.HandleNotify's tx path — not just "an
// interface showed up" the way TestExecMicroVMBackendBoot's existing
// cloud-hypervisor check is content with.
//
// It needs root, iproute2, a real kernel (GOA_ITEST_HYPERVISOR_KERNEL)
// with virtio_net built in or already loaded (no initrd is given, so a
// module can't be loaded from one — the same assumption
// GOA_ITEST_MICROVM_KERNEL already makes for cloud-hypervisor's own
// virtio-net), and a `ping` binary, or this test skips.
func TestHypervisorBootNetworking(t *testing.T) {
	kernel := os.Getenv("GOA_ITEST_HYPERVISOR_KERNEL")
	if kernel == "" {
		t.Skip("GOA_ITEST_HYPERVISOR_KERNEL not set")
	}
	if _, err := exec.LookPath("ping"); err != nil {
		t.Skip("ping not available")
	}

	ctx := context.Background()
	net := manager.NewExecBackend()
	const bridge = "br-itest-hv"
	if err := net.EnsureBridge(ctx, manager.Bridge{Name: bridge, CIDR: "10.125.0.0/24"}); err != nil {
		t.Fatalf("ensure bridge: %v", err)
	}
	t.Cleanup(func() { _ = net.DeleteBridge(ctx, bridge) })
	if err := net.EnsureAddress(ctx, bridge, "10.125.0.1/24"); err != nil {
		t.Fatalf("ensure bridge address: %v", err)
	}

	const tap = "tap-itest-hv"
	if out, err := exec.Command("ip", "tuntap", "add", "dev", tap, "mode", "tap").CombinedOutput(); err != nil {
		t.Fatalf("create tap: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("ip", "link", "del", tap).Run() })
	if out, err := exec.Command("ip", "link", "set", tap, "master", bridge).CombinedOutput(); err != nil {
		t.Fatalf("attach tap to bridge: %v: %s", err, out)
	}
	if out, err := exec.Command("ip", "link", "set", tap, "up").CombinedOutput(); err != nil {
		t.Fatalf("bring tap up: %v: %s", err, out)
	}

	m, err := hypervisor.New(hypervisor.Config{
		VCPUs:      1,
		MemoryMB:   256,
		KernelPath: kernel,
		CmdLine:    "console=ttyS0 panic=-1 ip=10.125.0.10::10.125.0.1:255.255.255.0::eth0:off",
		TapName:    tap,
		MAC:        "02:00:00:00:00:10",
	})
	if err != nil {
		skipIfKVMUnusable(t, err)
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = m.Close() }()

	runCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() { _ = m.Run(runCtx, nil) }()

	// Give the guest kernel time to reach its ip= network bring-up
	// (well before any rootfs is needed), then retry a ping a few times
	// — there is no signal from the guest side to synchronize on more
	// precisely than this.
	var pingErr error
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command("ping", "-c", "1", "-W", "2", "10.125.0.10").CombinedOutput()
		if err == nil {
			pingErr = nil
			break
		}
		pingErr = fmt.Errorf("%w: %s", err, out)
		time.Sleep(1 * time.Second)
	}
	if pingErr != nil {
		t.Fatalf("guest never answered a ping within the deadline: %v", pingErr)
	}
}
