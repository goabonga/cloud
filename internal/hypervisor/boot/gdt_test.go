// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package boot_test

import (
	"bytes"
	"testing"

	"github.com/goabonga/infrastructure/internal/hypervisor/boot"
)

func TestBuildGDT(t *testing.T) {
	gdt := boot.BuildGDT()
	if len(gdt) != 24 {
		t.Fatalf("len(gdt) = %d, want 24", len(gdt))
	}

	null := gdt[0:8]
	if !bytes.Equal(null, make([]byte, 8)) {
		t.Errorf("null descriptor = % x, want all zero", null)
	}

	// Flat 64-bit code segment: limit=0xfffff (G=1), base=0,
	// access=0x9a (present, ring0, code, execute/read),
	// flags|limit_hi=0xaf (G=1,L=1, limit bits 16:19=0xf).
	code := gdt[8:16]
	wantCode := []byte{0xff, 0xff, 0x00, 0x00, 0x00, 0x9a, 0xaf, 0x00}
	if !bytes.Equal(code, wantCode) {
		t.Errorf("code descriptor = % x, want % x", code, wantCode)
	}

	// Flat data segment: access=0x92 (present, ring0, data, read/write),
	// flags|limit_hi=0xcf (G=1, D/B=1, limit bits 16:19=0xf).
	data := gdt[16:24]
	wantData := []byte{0xff, 0xff, 0x00, 0x00, 0x00, 0x92, 0xcf, 0x00}
	if !bytes.Equal(data, wantData) {
		t.Errorf("data descriptor = % x, want % x", data, wantData)
	}
}
