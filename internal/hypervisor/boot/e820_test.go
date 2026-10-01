// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package boot_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/hypervisor/boot"
)

func TestBuildE820(t *testing.T) {
	const memSize = 256 << 20 // 256 MiB
	entries, err := boot.BuildE820(memSize)
	if err != nil {
		t.Fatalf("BuildE820: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
	if entries[0].Addr != 0 || entries[0].Size != 0x9fc00 || entries[0].Type != boot.E820TypeRAM {
		t.Errorf("entries[0] = %+v, want {0, 0x9fc00, RAM}", entries[0])
	}
	if entries[1].Addr != boot.KernelLoadAddr {
		t.Errorf("entries[1].Addr = 0x%x, want 0x%x (KernelLoadAddr)", entries[1].Addr, boot.KernelLoadAddr)
	}
	wantSize := uint64(memSize) - boot.KernelLoadAddr
	if entries[1].Size != wantSize {
		t.Errorf("entries[1].Size = %d, want %d", entries[1].Size, wantSize)
	}
	if entries[1].Type != boot.E820TypeRAM {
		t.Errorf("entries[1].Type = %d, want RAM", entries[1].Type)
	}

	// Both entries must stay within memSize and never overlap the gap
	// between them (the implicitly-reserved EBDA/VGA/option-ROM region).
	if entries[0].Addr+entries[0].Size > entries[1].Addr {
		t.Errorf("low-memory entry (ends at 0x%x) overlaps the kernel-load entry (starts at 0x%x)",
			entries[0].Addr+entries[0].Size, entries[1].Addr)
	}
	if entries[1].Addr+entries[1].Size != memSize {
		t.Errorf("high entry ends at 0x%x, want memSize 0x%x", entries[1].Addr+entries[1].Size, memSize)
	}
}

func TestBuildE820RejectsTooLittleMemory(t *testing.T) {
	if _, err := boot.BuildE820(boot.KernelLoadAddr); err == nil {
		t.Fatal("BuildE820 succeeded with memory == KernelLoadAddr (no room for a kernel), want error")
	}
	if _, err := boot.BuildE820(1 << 20); err == nil {
		t.Fatal("BuildE820 succeeded with 1 MiB of memory, want error")
	}
}
