// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

//go:build integration

package integration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/hypervisor"
	"github.com/goabonga/infrastructure/internal/hypervisor/kvm"
)

// TestHypervisorBootReachesExit boots a single vCPU against a real kernel
// under the 64-bit boot protocol and asserts it actually starts executing
// kernel code — observed as a real (not immediately fatal) vCPU exit —
// rather than triple-faulting on the first instruction. It does not expect
// a shell prompt: there is no console device yet (added in a later
// milestone), so the guest runs blind and this test only proves the
// boot-protocol register/page-table/GDT setup in internal/hypervisor got
// the vCPU into long mode correctly.
//
// It needs root (or kvm-group membership) and a real kernel: point
// GOA_ITEST_HYPERVISOR_KERNEL at a bzImage, e.g. /boot/vmlinuz-$(uname -r)
// on most distributions, or this test skips.
func TestHypervisorBootReachesExit(t *testing.T) {
	if _, err := os.Stat(kvm.DevicePath); err != nil {
		t.Skipf("%s not available: %v", kvm.DevicePath, err)
	}
	kernel := os.Getenv("GOA_ITEST_HYPERVISOR_KERNEL")
	if kernel == "" {
		t.Skip("GOA_ITEST_HYPERVISOR_KERNEL not set")
	}

	m, err := hypervisor.New(hypervisor.Config{
		VCPUs:      1,
		MemoryMB:   256,
		KernelPath: kernel,
		CmdLine:    "console=ttyS0 panic=-1",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var events int
	err = m.Run(ctx, func(e *hypervisor.ExitEvent) {
		events++
		if events == 1 {
			cancel() // stop as soon as we've observed real guest execution
		}
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if events == 0 {
		t.Fatal("no vcpu exit observed within the timeout — the vcpu likely never started executing guest code")
	}
}
