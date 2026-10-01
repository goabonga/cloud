// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package boot_test

import (
	"encoding/binary"
	"testing"

	"github.com/goabonga/infrastructure/internal/hypervisor/boot"
)

func TestBuildBootParams(t *testing.T) {
	data := fakeBzImage(t, 4, 0x020f, 4096, 0x7fffffff, 64)
	img, err := boot.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	const memSize = 256 << 20
	e820, err := boot.BuildE820(memSize)
	if err != nil {
		t.Fatalf("BuildE820: %v", err)
	}

	const cmdlineAddr = 0x20000
	const ramdiskAddr = 0x0f000000
	const ramdiskSize = 0x00100000

	zp, err := img.BuildBootParams(cmdlineAddr, ramdiskAddr, ramdiskSize, e820)
	if err != nil {
		t.Fatalf("BuildBootParams: %v", err)
	}
	if len(zp) != 0x1000 {
		t.Fatalf("len(zp) = %d, want 4096", len(zp))
	}

	if got := zp[0x1e8]; got != byte(len(e820)) {
		t.Errorf("e820_entries = %d, want %d", got, len(e820))
	}
	for i, e := range e820 {
		off := 0x2d0 + i*20
		gotAddr := binary.LittleEndian.Uint64(zp[off:])
		gotSize := binary.LittleEndian.Uint64(zp[off+8:])
		gotType := binary.LittleEndian.Uint32(zp[off+16:])
		if gotAddr != e.Addr || gotSize != e.Size || gotType != uint32(e.Type) {
			t.Errorf("e820[%d] = {0x%x, 0x%x, %d}, want {0x%x, 0x%x, %d}",
				i, gotAddr, gotSize, gotType, e.Addr, e.Size, e.Type)
		}
	}

	if got := zp[0x210]; got != 0xff {
		t.Errorf("type_of_loader = 0x%02x, want 0xff", got)
	}
	if zp[0x211]&(1<<6) == 0 {
		t.Error("loadflags KEEP_SEGMENTS bit (0x40) not set")
	}
	if got := binary.LittleEndian.Uint32(zp[0x228:]); got != cmdlineAddr {
		t.Errorf("cmd_line_ptr = 0x%x, want 0x%x", got, cmdlineAddr)
	}
	if got := binary.LittleEndian.Uint32(zp[0x218:]); got != ramdiskAddr {
		t.Errorf("ramdisk_image = 0x%x, want 0x%x", got, ramdiskAddr)
	}
	if got := binary.LittleEndian.Uint32(zp[0x21c:]); got != ramdiskSize {
		t.Errorf("ramdisk_size = 0x%x, want 0x%x", got, ramdiskSize)
	}

	// BuildBootParams must not mutate img.Header itself, so a second call
	// (e.g. after memory size changes) starts from the same unpatched
	// header rather than compounding a previous patch.
	if got := img.Header[0x210-0x1f1]; got == 0xff {
		t.Error("BuildBootParams mutated img.Header in place")
	}
}

func TestBuildBootParamsRejectsEmptyE820(t *testing.T) {
	data := fakeBzImage(t, 4, 0x020f, 0, 0, 16)
	img, err := boot.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := img.BuildBootParams(0, 0, 0, nil); err == nil {
		t.Fatal("BuildBootParams succeeded with no E820 entries, want error")
	}
}
