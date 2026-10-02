// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package boot_test

import (
	"encoding/binary"
	"testing"

	"github.com/goabonga/infrastructure/internal/hypervisor/boot"
)

func TestBuildPageTablesOneGB(t *testing.T) {
	pages, err := boot.BuildPageTables(256 << 20) // well under 1 GiB
	if err != nil {
		t.Fatalf("BuildPageTables: %v", err)
	}
	if len(pages) != 3 {
		t.Fatalf("len(pages) = %d, want 3 (PML4 + PDPT + 1 PD)", len(pages))
	}

	pml4, ok := pages[boot.PML4Addr]
	if !ok || len(pml4) != 0x1000 {
		t.Fatalf("pages[PML4Addr] missing or wrong size: %d bytes", len(pml4))
	}
	if got := binary.LittleEndian.Uint64(pml4[0:]); got != boot.PDPTAddr|3 {
		t.Errorf("PML4[0] = 0x%x, want 0x%x (PDPTAddr|present|write)", got, boot.PDPTAddr|3)
	}
	for i := 8; i < len(pml4); i += 8 {
		if binary.LittleEndian.Uint64(pml4[i:]) != 0 {
			t.Fatalf("PML4[%d] is non-zero, want only entry 0 populated", i/8)
		}
	}

	pdpt, ok := pages[boot.PDPTAddr]
	if !ok {
		t.Fatal("pages[PDPTAddr] missing")
	}
	if got := binary.LittleEndian.Uint64(pdpt[0:]); got != boot.PDAddr|3 {
		t.Errorf("PDPT[0] = 0x%x, want 0x%x (PDAddr|present|write)", got, boot.PDAddr|3)
	}

	pd, ok := pages[boot.PDAddr]
	if !ok {
		t.Fatal("pages[PDAddr] missing")
	}
	const pdeFlags = 0x1 | 0x2 | 0x80 // present | write | huge (PS)
	cases := []struct{ entry, wantAddr uint64 }{
		{0, 0},
		{1, 2 << 20},
		{511, 511 * (2 << 20)},
	}
	for _, c := range cases {
		got := binary.LittleEndian.Uint64(pd[c.entry*8:])
		want := c.wantAddr | pdeFlags
		if got != want {
			t.Errorf("PD[%d] = 0x%x, want 0x%x", c.entry, got, want)
		}
	}
}

func TestBuildPageTablesSpansMultipleGB(t *testing.T) {
	pages, err := boot.BuildPageTables(1536 << 20) // 1.5 GiB -> 2 PD pages
	if err != nil {
		t.Fatalf("BuildPageTables: %v", err)
	}
	if len(pages) != 4 {
		t.Fatalf("len(pages) = %d, want 4 (PML4 + PDPT + 2 PD)", len(pages))
	}
	secondPD, ok := pages[boot.PDAddr+0x1000]
	if !ok {
		t.Fatal("pages[PDAddr+0x1000] (second PD page) missing")
	}
	// Entry 0 of the second PD page maps the start of the second GiB.
	const pdeFlags = 0x1 | 0x2 | 0x80
	if got := binary.LittleEndian.Uint64(secondPD[0:]); got != (1<<30)|pdeFlags {
		t.Errorf("second PD[0] = 0x%x, want 0x%x", got, (1<<30)|pdeFlags)
	}

	pdpt := pages[boot.PDPTAddr]
	if got := binary.LittleEndian.Uint64(pdpt[8:]); got != (boot.PDAddr+0x1000)|3 {
		t.Errorf("PDPT[1] = 0x%x, want 0x%x", got, (boot.PDAddr+0x1000)|3)
	}
}

func TestBuildPageTablesRejectsExcessiveMemory(t *testing.T) {
	// Enough GiB that the PD run would run into CmdlineAddr.
	if _, err := boot.BuildPageTables(64 << 30); err == nil {
		t.Fatal("BuildPageTables succeeded with 64 GiB, want error (PD run would overlap CmdlineAddr)")
	}
}
