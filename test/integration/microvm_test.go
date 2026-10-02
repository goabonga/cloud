// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/manager"
)

// TestExecMicroVMBackendBoot brings a real micro-VM up on a TAP-attached
// bridge, under infra-hypervisor, and tears it down. It needs root,
// iproute2, a working `go build` toolchain (infra-hypervisor isn't
// expected pre-installed the way cloud-hypervisor once was — it's built
// here the same way hypervisor_cmd_test.go's TestHypervisorBoot builds
// cmd/hypervisor directly), real /dev/kvm access, and a kernel (there is
// no kernel fetch/caching): point GOA_ITEST_MICROVM_KERNEL at one and
// GOA_ITEST_MICROVM_DISK at a boot image (a URL or a local path; either is
// fetched/cloned into the node-local cache) with virtio_net/virtio_blk
// built in (no initrd, so neither can load as a module), or this test
// skips.
func TestExecMicroVMBackendBoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	if _, err := exec.LookPath("ip"); err != nil {
		t.Skip("iproute2 not available")
	}
	kernel := os.Getenv("GOA_ITEST_MICROVM_KERNEL")
	disk := os.Getenv("GOA_ITEST_MICROVM_DISK")
	if kernel == "" || disk == "" {
		t.Skip("GOA_ITEST_MICROVM_KERNEL and GOA_ITEST_MICROVM_DISK not set")
	}

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "infra-hypervisor")
	build := exec.Command("go", "build", "-o", binPath, "github.com/goabonga/infrastructure/cmd/hypervisor")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build cmd/hypervisor: %v: %s", err, out)
	}
	// ExecMicroVMBackend resolves "infra-hypervisor" through PATH, the
	// same way it would find a .deb-installed /usr/bin/infra-hypervisor
	// in production (see packaging/build-debs.sh) — prepending binDir
	// here needs no backend API change to point it at the one just built.
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx := context.Background()
	net := manager.NewExecBackend()
	const bridge = "br-itest-v"
	if err := net.EnsureBridge(ctx, manager.Bridge{Name: bridge, CIDR: "10.124.0.0/16"}); err != nil {
		t.Fatalf("ensure bridge: %v", err)
	}
	t.Cleanup(func() { _ = net.DeleteBridge(ctx, bridge) })

	be := manager.NewExecMicroVMBackend(t.TempDir())
	const uid = "vm-itest"
	t.Cleanup(func() { _ = be.DeleteMicroVM(ctx, manager.MicroVMTeardown{UID: uid}) })

	res, err := be.EnsureMicroVM(ctx, manager.MicroVMRequest{
		UID:        uid,
		VCPUs:      1,
		MemoryMB:   256,
		KernelPath: kernel,
		Image:      disk,
		Bridge:     bridge,
		IP:         "10.124.0.10",
		Prefix:     16,
		Gateway:    "10.124.0.1",
	})
	if err != nil {
		t.Fatalf("ensure microvm: %v", err)
	}
	if res.Pid == 0 {
		t.Fatal("no infra-hypervisor pid recorded")
	}

	time.Sleep(500 * time.Millisecond) // let infra-hypervisor attach the tap
	out, err := exec.Command("ip", "link", "show", res.Tap).CombinedOutput()
	if err != nil || !strings.Contains(string(out), res.Tap) {
		t.Fatalf("tap %q not present: %v: %s", res.Tap, err, out)
	}

	if err := be.DeleteMicroVM(ctx, manager.MicroVMTeardown{UID: uid, Tap: res.Tap}); err != nil {
		t.Fatalf("delete microvm: %v", err)
	}
	out, _ = exec.Command("ip", "link", "show", res.Tap).CombinedOutput()
	if strings.Contains(string(out), res.Tap) {
		t.Fatalf("tap %q not removed: %s", res.Tap, out)
	}
}
