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

// TestExecDiskBackendEncrypted provisions a real LUKS-encrypted disk image and
// tears it down. It needs root, cryptsetup and mkfs.ext4, and skips otherwise.
func TestExecDiskBackendEncrypted(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}
	for _, tool := range []string{"cryptsetup", "mkfs.ext4"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}

	ctx := context.Background()
	be := manager.NewExecDiskBackend(t.TempDir())
	const uid = "disk-itest"
	t.Cleanup(func() { _ = be.DeleteDisk(ctx, uid) })

	passphrase := []byte("0123456789abcdef0123456789abcdef")
	path, err := be.EnsureDisk(ctx, manager.DiskRequest{UID: uid, SizeMB: 32, Passphrase: passphrase})
	if err != nil {
		t.Fatalf("ensure encrypted disk: %v", err)
	}
	if path != "/dev/mapper/infra-"+uid {
		t.Fatalf("path = %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("mapper device missing: %v", err)
	}

	if err := be.DeleteDisk(ctx, uid); err != nil {
		t.Fatalf("delete disk: %v", err)
	}
}
