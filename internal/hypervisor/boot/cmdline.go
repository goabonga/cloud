// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package boot

import "fmt"

// legacyCmdlineMax is the command-line length every kernel supports, used
// when the running kernel predates the cmdline_size header field
// (protocol < 2.06) and so didn't advertise a larger one itself.
const legacyCmdlineMax = 255

// BuildCmdline returns s as a NUL-terminated byte string, erroring if it
// doesn't fit in the smaller of CmdlineMaxSize (this package's own budget
// for the page at CmdlineAddr) and kernelMax (the kernel's own advertised
// limit — img.CmdlineSize from Parse, or 0 for a kernel too old to
// advertise one, in which case legacyCmdlineMax applies).
func BuildCmdline(s string, kernelMax uint32) ([]byte, error) {
	max := uint32(CmdlineMaxSize)
	if kernelMax == 0 {
		kernelMax = legacyCmdlineMax
	}
	if kernelMax < max {
		max = kernelMax
	}
	if uint32(len(s)) >= max {
		return nil, fmt.Errorf("boot: command line is %d bytes, max %d", len(s), max-1)
	}
	b := make([]byte, len(s)+1)
	copy(b, s)
	return b, nil
}

// legacyInitrdAddrMax is used when the running kernel predates the
// initrd_addr_max header field (protocol < 2.03): the historical safe
// ceiling documented for such kernels.
const legacyInitrdAddrMax = 0x37ffffff

// PlaceInitrd picks a page-aligned guest-physical address for an initrd of
// initrdLen bytes: as high in memory as possible, so it never collides
// with the kernel loaded at KernelLoadAddr, while staying at or below
// maxAddr (img.InitrdAddrMax from Parse, or 0 for a kernel too old to
// advertise one, in which case legacyInitrdAddrMax applies) and within
// memSize.
func PlaceInitrd(memSize uint64, initrdLen int, maxAddr uint32) (uint64, error) {
	if initrdLen == 0 {
		return 0, nil
	}
	ceiling := memSize
	if maxAddr == 0 {
		maxAddr = legacyInitrdAddrMax
	}
	if uint64(maxAddr)+1 < ceiling {
		ceiling = uint64(maxAddr) + 1
	}

	if uint64(initrdLen) > ceiling {
		return 0, fmt.Errorf("boot: %d-byte initrd does not fit below 0x%x with %d bytes of memory", initrdLen, maxAddr+1, memSize)
	}
	addr := ceiling - uint64(initrdLen)
	addr &^= 0xfff // page-align down

	if addr < KernelLoadAddr {
		return 0, fmt.Errorf("boot: %d-byte initrd does not fit below 0x%x with %d bytes of memory", initrdLen, maxAddr+1, memSize)
	}
	return addr, nil
}
