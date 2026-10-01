// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package boot

import (
	"encoding/binary"
	"fmt"
)

// E820Type is a struct boot_e820_entry type value (arch/x86/include/uapi/asm/e820.h).
type E820Type uint32

const (
	E820TypeRAM   E820Type = 1
	e820EntrySize          = 20 // addr u64 + size u64 + type u32, packed
)

// E820Entry mirrors struct boot_e820_entry.
type E820Entry struct {
	Addr uint64
	Size uint64
	Type E820Type
}

func (e E820Entry) marshal(b []byte) {
	binary.LittleEndian.PutUint64(b[0:], e.Addr)
	binary.LittleEndian.PutUint64(b[8:], e.Size)
	binary.LittleEndian.PutUint32(b[16:], uint32(e.Type))
}

// BuildE820 maps memSize bytes of guest RAM starting at address 0 into the
// two-region layout minimal x86-64 VMMs conventionally use: [0, MPTableAddr)
// (conventional low memory, below the EBDA/VGA/option-ROM area) and
// [1 MiB, memSize) (where the kernel, initrd and every structure this
// package builds are actually placed). The [MPTableAddr, 1 MiB) gap — where
// mptable.go writes the MP table — is left out of the map entirely
// (implicitly reserved) rather than marked reserved, matching how real
// BIOS/UEFI E820 maps treat it.
//
// memSize must be greater than KernelLoadAddr (1 MiB) — there is nowhere
// to put the kernel otherwise.
func BuildE820(memSize uint64) ([]E820Entry, error) {
	if memSize <= KernelLoadAddr {
		return nil, fmt.Errorf("boot: %d bytes of memory is not enough to load a kernel at 0x%x", memSize, KernelLoadAddr)
	}
	return []E820Entry{
		{Addr: 0, Size: MPTableAddr, Type: E820TypeRAM},
		{Addr: KernelLoadAddr, Size: memSize - KernelLoadAddr, Type: E820TypeRAM},
	}, nil
}
