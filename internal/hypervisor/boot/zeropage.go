// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package boot

import "fmt"

// Byte offsets into struct boot_params (arch/x86/include/uapi/asm/bootparam.h),
// the "zero page" a kernel expects %rsi to point at on entry. Only the
// fields this minimal boot path actually needs are named; everything else
// in the 4 KiB page this package builds is left zeroed, which is the
// documented requirement for every field a bootloader doesn't explicitly
// set (hence "zero page").
const (
	zpE820Entries    = 0x1e8 // __u8, number of valid entries in zpE820Table
	zpE820MaxEntries = 128   // E820_MAX_ENTRIES_ZEROPAGE
	zpE820Table      = 0x2d0 // struct boot_e820_entry[zpE820MaxEntries]
	zpSize           = 0x1000
)

// BuildBootParams assembles the zero page: img's setup_header, patched with
// the given command-line and initrd pointers, plus the E820 memory map.
// The result is exactly one 4 KiB page, meant to be written at
// BootParamsAddr.
func (img *Image) BuildBootParams(cmdlineAddr uint32, ramdiskAddr, ramdiskSize uint32, e820 []E820Entry) ([]byte, error) {
	if len(e820) == 0 {
		return nil, fmt.Errorf("boot: BuildBootParams: empty E820 map")
	}
	if len(e820) > zpE820MaxEntries {
		return nil, fmt.Errorf("boot: BuildBootParams: %d E820 entries, max %d", len(e820), zpE820MaxEntries)
	}
	if len(img.Header) != headerEnd-headerStart {
		return nil, fmt.Errorf("boot: BuildBootParams: header is %d bytes, want %d (did Parse build it?)", len(img.Header), headerEnd-headerStart)
	}

	header := append([]byte(nil), img.Header...)
	(&Image{Header: header}).patchForDirectBoot(cmdlineAddr, ramdiskAddr, ramdiskSize)

	zp := make([]byte, zpSize)
	copy(zp[headerStart:headerEnd], header)
	zp[zpE820Entries] = byte(len(e820)) // #nosec G115 -- bounded to [1, zpE820MaxEntries] (128) by the check above
	for i, e := range e820 {
		e.marshal(zp[zpE820Table+i*e820EntrySize:])
	}
	return zp, nil
}
