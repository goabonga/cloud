// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/goabonga/infrastructure/internal/manager"
)

// TestExecMicroVMBackendResolvesLocalImage exercises the image cache's local
// (non-URL) path through EnsureMicroVM: the source file is cloned into the
// cache and then into the instance's own disk, both readable with the
// original content, before the (expected) failure to start infra-hypervisor.
func TestExecMicroVMBackendResolvesLocalImage(t *testing.T) {
	t.Parallel()

	sim := newTapSim()
	dir := t.TempDir()
	be := manager.NewExecMicroVMBackendWithRunner(dir, sim.run)

	src := filepath.Join(t.TempDir(), "base.raw")
	const content = "a fake raw disk image"
	if err := os.WriteFile(src, []byte(content), 0o600); err != nil {
		t.Fatalf("seed source image: %v", err)
	}

	req := manager.MicroVMRequest{
		UID:        "vm-1",
		Bridge:     "br-vpc1",
		IP:         "10.0.1.10",
		Prefix:     24,
		Gateway:    "10.0.1.1",
		VCPUs:      1,
		MemoryMB:   512,
		KernelPath: "/boot/vmlinux",
		Image:      src,
	}
	if _, err := be.EnsureMicroVM(context.Background(), req); err == nil {
		t.Fatal("expected an error: no infra-hypervisor binary in this test environment")
	}

	instancePath := filepath.Join(dir, "microvm", "vm-1", "disk.raw")
	got, err := os.ReadFile(instancePath) // #nosec G304 -- test-owned path
	if err != nil {
		t.Fatalf("read instance disk: %v", err)
	}
	if string(got) != content {
		t.Fatalf("instance disk content = %q, want %q", got, content)
	}
}

// TestExecMicroVMBackendFetchesImageOnce downloads a boot image over HTTP
// once, even across two instances that reference the same URL, and clones it
// into each instance's own disk.
func TestExecMicroVMBackendFetchesImageOnce(t *testing.T) {
	t.Parallel()

	const content = "a fake raw disk image served over http"
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(content))
	}))
	defer srv.Close()

	sim := newTapSim()
	dir := t.TempDir()
	be := manager.NewExecMicroVMBackendWithRunner(dir, sim.run)

	for _, uid := range []string{"vm-1", "vm-2"} {
		req := manager.MicroVMRequest{
			UID:        uid,
			Bridge:     "br-vpc1",
			IP:         "10.0.1.10",
			Prefix:     24,
			Gateway:    "10.0.1.1",
			VCPUs:      1,
			MemoryMB:   512,
			KernelPath: "/boot/vmlinux",
			Image:      srv.URL,
		}
		if _, err := be.EnsureMicroVM(context.Background(), req); err == nil {
			t.Fatalf("%s: expected an error: no infra-hypervisor binary in this test environment", uid)
		}
	}

	if got := hits.Load(); got != 1 {
		t.Fatalf("server hit %d times, want 1 (the second instance should reuse the cache)", got)
	}
	for _, uid := range []string{"vm-1", "vm-2"} {
		instancePath := filepath.Join(dir, "microvm", uid, "disk.raw")
		got, err := os.ReadFile(instancePath) // #nosec G304 -- test-owned path
		if err != nil {
			t.Fatalf("%s: read instance disk: %v", uid, err)
		}
		if string(got) != content {
			t.Fatalf("%s: instance disk content = %q, want %q", uid, got, content)
		}
	}
}
