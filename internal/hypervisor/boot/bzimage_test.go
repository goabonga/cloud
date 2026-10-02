// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package boot_test

import (
	"encoding/binary"
	"testing"

	"github.com/goabonga/infrastructure/internal/hypervisor/boot"
)

// fakeBzImage builds a synthetic bzImage with a valid boot sector and
// setup_header, and codeLen bytes of recognizable (0, 1, 2, ...) "kernel
// code" after the setup region, for tests that don't need a real kernel.
func fakeBzImage(t *testing.T, setupSects int, version uint16, cmdlineSize, initrdAddrMax uint32, codeLen int) []byte {
	t.Helper()
	setupSize := (setupSects + 1) * 512
	data := make([]byte, setupSize+codeLen)

	data[0x1f1] = byte(setupSects)
	binary.LittleEndian.PutUint16(data[0x1fe:], 0xaa55)
	binary.LittleEndian.PutUint32(data[0x202:], 0x53726448) // "HdrS"
	binary.LittleEndian.PutUint16(data[0x206:], version)
	if version >= 0x0203 {
		binary.LittleEndian.PutUint32(data[0x22c:], initrdAddrMax)
	}
	if version >= 0x0206 {
		binary.LittleEndian.PutUint32(data[0x238:], cmdlineSize)
	}
	for i := 0; i < codeLen; i++ {
		data[setupSize+i] = byte(i)
	}
	return data
}

func TestParse(t *testing.T) {
	data := fakeBzImage(t, 4, 0x020f, 4096, 0x7fffffff, 256)

	img, err := boot.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if img.Version != 0x020f {
		t.Errorf("Version = 0x%04x, want 0x020f", img.Version)
	}
	if img.CmdlineSize != 4096 {
		t.Errorf("CmdlineSize = %d, want 4096", img.CmdlineSize)
	}
	if img.InitrdAddrMax != 0x7fffffff {
		t.Errorf("InitrdAddrMax = 0x%x, want 0x7fffffff", img.InitrdAddrMax)
	}
	if len(img.Code) != 256 {
		t.Fatalf("len(Code) = %d, want 256", len(img.Code))
	}
	for i, b := range img.Code {
		if b != byte(i) {
			t.Fatalf("Code[%d] = %d, want %d", i, b, byte(i))
		}
	}
}

func TestParseSetupSectsZeroDefaultsToFour(t *testing.T) {
	// setup_sects=0 means "4" (Documentation/x86/boot.rst), i.e. a
	// 5*512=2560-byte setup region; a file laid out for setup_sects=0
	// taken literally (1*512=512-byte setup region plus a little code)
	// is too short once Parse applies the default, and must be rejected
	// rather than silently parsing those extra bytes as kernel code.
	data := fakeBzImage(t, 0, 0x020f, 0, 0, 200)
	if _, err := boot.Parse(data); err == nil {
		t.Fatal("Parse succeeded on a file too short for the defaulted setup_sects=4, want error")
	}
}

func TestParseRejectsBadBootFlag(t *testing.T) {
	data := fakeBzImage(t, 4, 0x020f, 0, 0, 16)
	binary.LittleEndian.PutUint16(data[0x1fe:], 0x0000)
	if _, err := boot.Parse(data); err == nil {
		t.Fatal("Parse succeeded with a corrupt boot flag, want error")
	}
}

func TestParseRejectsBadHeaderMagic(t *testing.T) {
	data := fakeBzImage(t, 4, 0x020f, 0, 0, 16)
	binary.LittleEndian.PutUint32(data[0x202:], 0)
	if _, err := boot.Parse(data); err == nil {
		t.Fatal("Parse succeeded with a corrupt header magic, want error")
	}
}

func TestParseLegacyHeaderHasNoCmdlineSizeOrInitrdMax(t *testing.T) {
	// Protocol 2.02, predating both fields (2.03 and 2.06 respectively).
	data := fakeBzImage(t, 4, 0x0202, 0, 0, 16)
	img, err := boot.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if img.CmdlineSize != 0 {
		t.Errorf("CmdlineSize = %d on a pre-2.06 header, want 0", img.CmdlineSize)
	}
	if img.InitrdAddrMax != 0 {
		t.Errorf("InitrdAddrMax = %d on a pre-2.03 header, want 0", img.InitrdAddrMax)
	}
}
