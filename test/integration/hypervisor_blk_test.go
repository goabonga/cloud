// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

//go:build integration

package integration

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/hypervisor"
)

// TestHypervisorBootDisk proves real disk I/O flows through virtio-blk
// against a real kernel: attaches a plain (not a real filesystem, just a
// recognizable byte pattern) raw disk file as /dev/vda and asks the kernel
// to mount it as root. There is no init to run (no bootable filesystem
// image is built here — that needs real tooling like mkfs/debootstrap
// this test doesn't try to replicate), so the mount necessarily fails,
// but getting to "VFS: Unable to mount root fs" at all means the kernel's
// virtio_blk driver successfully probed the device (config space: our
// capacity) and performed real reads against it (superblock detection)
// through Blk.HandleNotify's request-processing path — not just that an
// interface appeared, the way this milestone's unit tests already proved
// without a real kernel. A full read-back-the-exact-bytes-from-userspace
// proof needs a prepared bootable disk image (with an init), which is a
// manual verification step documented in
// docs/architecture/go-hypervisor.md, not something this automated test
// attempts.
//
// It needs root (or kvm-group membership) and a real kernel
// (GOA_ITEST_HYPERVISOR_KERNEL) with virtio_blk built in (no initrd is
// given, so a module can't be loaded from one), or this test skips.
func TestHypervisorBootDisk(t *testing.T) {
	kernel := os.Getenv("GOA_ITEST_HYPERVISOR_KERNEL")
	if kernel == "" {
		t.Skip("GOA_ITEST_HYPERVISOR_KERNEL not set")
	}

	diskPath := filepath.Join(t.TempDir(), "disk.raw")
	pattern := bytes.Repeat([]byte("GOHV"), 4*1024*1024/4) // 16 MiB, recognizable but not a real filesystem
	if err := os.WriteFile(diskPath, pattern, 0o644); err != nil {
		t.Fatalf("write disk image: %v", err)
	}

	var console bytes.Buffer
	m, err := hypervisor.New(hypervisor.Config{
		VCPUs:      1,
		MemoryMB:   256,
		KernelPath: kernel,
		CmdLine:    "console=ttyS0 panic=-1 root=/dev/vda ro",
		DiskPath:   diskPath,
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

	out := console.String()
	if !strings.Contains(out, "vda") {
		t.Fatalf("console output never mentions vda (virtio_blk probe likely failed); got %d bytes:\n%s", len(out), out)
	}
	if !strings.Contains(out, "Unable to mount root fs") {
		t.Fatalf("console output does not show the expected root-mount failure (no evidence the kernel read the disk); got %d bytes:\n%s", len(out), out)
	}
}
