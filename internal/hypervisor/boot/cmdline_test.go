// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package boot_test

import (
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/hypervisor/boot"
)

func TestBuildCmdline(t *testing.T) {
	b, err := boot.BuildCmdline("console=ttyS0 root=/dev/vda", 4096)
	if err != nil {
		t.Fatalf("BuildCmdline: %v", err)
	}
	want := "console=ttyS0 root=/dev/vda\x00"
	if string(b) != want {
		t.Errorf("BuildCmdline = %q, want %q", b, want)
	}
}

func TestBuildCmdlineLegacyMax(t *testing.T) {
	// kernelMax=0 (pre-2.06 header) falls back to the 255-byte legacy cap.
	s := strings.Repeat("a", 254)
	if _, err := boot.BuildCmdline(s, 0); err != nil {
		t.Fatalf("BuildCmdline(254 bytes, legacy cap): %v", err)
	}
	if _, err := boot.BuildCmdline(strings.Repeat("a", 255), 0); err == nil {
		t.Fatal("BuildCmdline(255 bytes, legacy cap) succeeded, want error (no room for the NUL terminator)")
	}
}

func TestBuildCmdlineRejectsTooLong(t *testing.T) {
	if _, err := boot.BuildCmdline(strings.Repeat("a", 100), 100); err == nil {
		t.Fatal("BuildCmdline succeeded with a command line exactly at kernelMax, want error (no room for the NUL terminator)")
	}
}

func TestPlaceInitrdEmpty(t *testing.T) {
	addr, err := boot.PlaceInitrd(256<<20, 0, 0)
	if err != nil {
		t.Fatalf("PlaceInitrd(0 bytes): %v", err)
	}
	if addr != 0 {
		t.Errorf("PlaceInitrd(0 bytes) = 0x%x, want 0", addr)
	}
}

func TestPlaceInitrd(t *testing.T) {
	const memSize = 256 << 20
	const initrdLen = 4 << 20
	addr, err := boot.PlaceInitrd(memSize, initrdLen, 0)
	if err != nil {
		t.Fatalf("PlaceInitrd: %v", err)
	}
	if addr%0x1000 != 0 {
		t.Errorf("PlaceInitrd address 0x%x is not page-aligned", addr)
	}
	if addr+uint64(initrdLen) > memSize {
		t.Errorf("PlaceInitrd address 0x%x + len %d overruns memSize 0x%x", addr, initrdLen, memSize)
	}
	if addr < boot.KernelLoadAddr {
		t.Errorf("PlaceInitrd address 0x%x is below KernelLoadAddr 0x%x", addr, boot.KernelLoadAddr)
	}
}

func TestPlaceInitrdRespectsAddrMax(t *testing.T) {
	const memSize = 256 << 20
	const initrdLen = 4 << 20
	const maxAddr = 32 << 20 // force a low ceiling well under memSize
	addr, err := boot.PlaceInitrd(memSize, initrdLen, maxAddr)
	if err != nil {
		t.Fatalf("PlaceInitrd: %v", err)
	}
	if addr+uint64(initrdLen) > maxAddr+1 {
		t.Errorf("PlaceInitrd address 0x%x + len %d exceeds maxAddr 0x%x", addr, initrdLen, maxAddr)
	}
}

func TestPlaceInitrdRejectsTooLarge(t *testing.T) {
	if _, err := boot.PlaceInitrd(2<<20, 4<<20, 0); err == nil {
		t.Fatal("PlaceInitrd succeeded with an initrd larger than memory, want error")
	}
}
