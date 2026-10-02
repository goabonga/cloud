// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package hypervisor

import (
	"fmt"
	"os"
	"testing"

	"github.com/goabonga/infrastructure/internal/hypervisor/boot"
)

func TestSetupDisk(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "disk")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if err := f.Truncate(4096); err != nil {
		t.Fatalf("Truncate: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	m := &Machine{mem: make([]byte, 0x1000)}
	param, err := m.setupDisk(Config{DiskPath: f.Name()})
	if err != nil {
		t.Fatalf("setupDisk: %v", err)
	}

	if m.disk == nil {
		t.Fatal("setupDisk did not set m.disk")
	}
	if len(m.mmioDevices) != 1 {
		t.Fatalf("len(m.mmioDevices) = %d, want 1", len(m.mmioDevices))
	}
	want := fmt.Sprintf(" virtio_mmio.device=%d@0x%x:16", boot.MMIODeviceSize, boot.MMIOBaseAddr)
	if param != want {
		t.Errorf("cmdline param = %q, want %q", param, want)
	}
}

func TestSetupDiskMissingFile(t *testing.T) {
	m := &Machine{mem: make([]byte, 0x1000)}
	if _, err := m.setupDisk(Config{DiskPath: "/nonexistent/disk/path"}); err == nil {
		t.Fatal("setupDisk succeeded on a missing file, want error")
	}
}
