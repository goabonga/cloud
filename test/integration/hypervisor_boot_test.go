// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

//go:build integration

package integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/hypervisor"
)

// skipIfKVMUnusable lets the caller treat New's error as a skip rather than
// a failure when it's EACCES or ENOENT — os.Stat(kvm.DevicePath) succeeds
// on a device node regardless of whether the caller can actually open it
// (see hypervisor_kvm_test.go's TestKVMOpen for the same fix), so the real
// gate has to be the open attempt itself, not a stat beforehand.
func skipIfKVMUnusable(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		t.Skipf("/dev/kvm not usable: %v", err)
	}
}

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
		skipIfKVMUnusable(t, err)
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

// TestHypervisorBootSerialOutput boots the same way, but lets the vcpu run
// for several seconds against the emulated COM1 UART and asserts the
// captured output contains the kernel's own boot banner — the first real,
// content-level proof this hypervisor can get a guest far enough to talk
// back, not just reach a non-fatal exit. No initrd is given, so the kernel
// is expected to eventually panic looking for a root filesystem; that's
// fine, it happens well after the banner and this test does not wait for
// it (and panic=-1 avoids an endless reboot loop holding the VM up for no
// reason past the timeout).
func TestHypervisorBootSerialOutput(t *testing.T) {
	kernel := os.Getenv("GOA_ITEST_HYPERVISOR_KERNEL")
	if kernel == "" {
		t.Skip("GOA_ITEST_HYPERVISOR_KERNEL not set")
	}

	var console bytes.Buffer
	m, err := hypervisor.New(hypervisor.Config{
		VCPUs:      1,
		MemoryMB:   256,
		KernelPath: kernel,
		CmdLine:    "console=ttyS0 panic=-1",
		Console:    &console,
	})
	if err != nil {
		skipIfKVMUnusable(t, err)
		t.Fatalf("New: %v", err)
	}
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := m.Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(console.String(), "Linux version") {
		t.Fatalf("console output does not contain the kernel boot banner; got %d bytes:\n%s", console.Len(), console.String())
	}
}

// TestHypervisorBootSMP boots with more than one vcpu and asserts the
// guest itself reports bringing them all up — the kernel's own SMP
// bring-up code logs "smp: Brought up ... N CPUs" once every vcpu beyond
// the boot one has responded to the INIT-SIPI-SIPI sequence the booted
// kernel issued (not anything this package drives itself; see Run's doc
// comment in runloop.go). This has been Linux's exact wording for a long
// time, but isn't a documented, stable interface — if a future kernel
// changes it, this test (not the hypervisor) needs updating.
func TestHypervisorBootSMP(t *testing.T) {
	kernel := os.Getenv("GOA_ITEST_HYPERVISOR_KERNEL")
	if kernel == "" {
		t.Skip("GOA_ITEST_HYPERVISOR_KERNEL not set")
	}

	const numCPUs = 2
	var console bytes.Buffer
	m, err := hypervisor.New(hypervisor.Config{
		VCPUs:      numCPUs,
		MemoryMB:   256,
		KernelPath: kernel,
		CmdLine:    "console=ttyS0 panic=-1",
		Console:    &console,
	})
	if err != nil {
		skipIfKVMUnusable(t, err)
		t.Fatalf("New: %v", err)
	}
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := m.Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := fmt.Sprintf("Brought up 1 node, %d CPUs", numCPUs)
	if !strings.Contains(console.String(), want) {
		t.Fatalf("console output does not contain %q; got %d bytes:\n%s", want, console.Len(), console.String())
	}
}
