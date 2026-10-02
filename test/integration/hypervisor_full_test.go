// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/hypervisor"
	"github.com/goabonga/infrastructure/internal/manager"
)

// TestHypervisorBootFull is the milestone 5 parity proof: multi-vcpu,
// virtio-net and virtio-blk together in one VM, the direct analogue of
// TestExecMicroVMBackendBoot (which proves the same combination against
// cloud-hypervisor) for this hypervisor instead. Each milestone's own
// test (TestHypervisorBootSMP, TestHypervisorBootNetworking,
// TestHypervisorBootDisk) already proves its piece in isolation; this one
// proves they don't interfere with each other running at once — a real
// risk this package's design doesn't rule out by construction (every
// vcpu's goroutine, the net device's ReadLoop, and whichever vcpu
// services a blk QueueNotify are all touching shared Machine state
// concurrently for the first time together here).
//
// It needs root, iproute2, a real kernel (GOA_ITEST_HYPERVISOR_KERNEL)
// with virtio_net and virtio_blk built in, and a `ping` binary, or this
// test skips.
func TestHypervisorBootFull(t *testing.T) {
	kernel := os.Getenv("GOA_ITEST_HYPERVISOR_KERNEL")
	if kernel == "" {
		t.Skip("GOA_ITEST_HYPERVISOR_KERNEL not set")
	}
	if _, err := exec.LookPath("ping"); err != nil {
		t.Skip("ping not available")
	}

	ctx := context.Background()
	net := manager.NewExecBackend()
	const bridge = "br-itest-full"
	if err := net.EnsureBridge(ctx, manager.Bridge{Name: bridge, CIDR: "10.127.0.0/24"}); err != nil {
		t.Fatalf("ensure bridge: %v", err)
	}
	t.Cleanup(func() { _ = net.DeleteBridge(ctx, bridge) })
	if err := net.EnsureAddress(ctx, bridge, "10.127.0.1/24"); err != nil {
		t.Fatalf("ensure bridge address: %v", err)
	}

	const tap = "tap-itest-full"
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

	diskPath := filepath.Join(t.TempDir(), "disk.raw")
	if err := os.WriteFile(diskPath, bytes.Repeat([]byte("GOHV"), 4*1024*1024/4), 0o644); err != nil {
		t.Fatalf("write disk image: %v", err)
	}

	baselineGoroutines := runtime.NumGoroutine()

	var console bytes.Buffer
	m, err := hypervisor.New(hypervisor.Config{
		VCPUs:      2,
		MemoryMB:   256,
		KernelPath: kernel,
		CmdLine:    "console=ttyS0 panic=-1 ip=10.127.0.10::10.127.0.1:255.255.255.0::eth0:off root=/dev/vda ro",
		TapName:    tap,
		MAC:        "02:00:00:00:00:20",
		DiskPath:   diskPath,
		Console:    &console,
	})
	if err != nil {
		skipIfKVMUnusable(t, err)
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = m.Close() }()

	runCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = m.Run(runCtx, nil)
	}()

	var pingErr error
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command("ping", "-c", "1", "-W", "2", "10.127.0.10").CombinedOutput()
		if err == nil {
			pingErr = nil
			break
		}
		pingErr = fmt.Errorf("%w: %s", err, out)
		time.Sleep(1 * time.Second)
	}
	if pingErr != nil {
		t.Errorf("guest never answered a ping within the deadline: %v", pingErr)
	}

	// Give the (by now likely already-failed) root mount and the SMP
	// bring-up message, both logged well before the ip= network comes
	// up, time to have made it into the console buffer regardless of
	// ping's own timing.
	time.Sleep(2 * time.Second)
	cancel()

	out := console.String()
	if !strings.Contains(out, "Brought up 1 node, 2 CPUs") {
		t.Errorf("console output does not show both vcpus brought up; got %d bytes:\n%s", len(out), out)
	}
	if !strings.Contains(out, "vda") || !strings.Contains(out, "Unable to mount root fs") {
		t.Errorf("console output does not show the expected virtio-blk probe/mount-failure; got %d bytes:\n%s", len(out), out)
	}

	// Goroutine-hygiene check: wait for every vcpu goroutine and
	// Net.ReadLoop (Run's errgroup-style group) to actually return
	// after cancellation, then Close, then confirm the goroutine count
	// settles back near its pre-boot baseline rather than leaking one
	// per vcpu/device. No goleak dependency in this repo (see
	// docs/architecture/go-hypervisor.md) - runtime.NumGoroutine()
	// deltas, polled with a little slack for the Go runtime's own
	// background goroutines to settle, is what the plan this effort
	// follows calls for instead.
	select {
	case <-runDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of ctx being cancelled")
	}
	if err := m.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	var after int
	for i := 0; i < 20; i++ {
		after = runtime.NumGoroutine()
		if after <= baselineGoroutines+1 { // +1: generous slack, not an exact budget
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if after > baselineGoroutines+1 {
		t.Errorf("goroutine count after Close = %d, want close to the pre-boot baseline of %d (possible leak)", after, baselineGoroutines)
	}
}
