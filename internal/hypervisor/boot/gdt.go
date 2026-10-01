// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package boot

import "encoding/binary"

// Selectors into the GDT this package builds: index 0 is the mandatory
// null descriptor, index 1 a flat 64-bit code segment, index 2 a flat data
// segment (shared by DS/ES/FS/GS/SS — the 64-bit boot protocol only
// requires a single consistent data segment, see vcpu.go's sreg setup in
// the hypervisor package).
const (
	NullSelector = 0x00
	CodeSelector = 0x08
	DataSelector = 0x10

	gdtSize = 3 * 8
)

// descriptor flag/access bits (Intel SDM vol 3, section 3.4.5).
const (
	segPresent    = 1 << 7
	segTypeS      = 1 << 4 // descriptor type: 1 = code/data, 0 = system
	segTypeCode   = 0xa    // execute, read, accessed-bit clear
	segTypeData   = 0x2    // read/write, accessed-bit clear
	segFlagG      = 1 << 3 // granularity: limit is in 4 KiB units
	segFlagL      = 1 << 1 // 64-bit code segment (code segments only)
	segFlagDB     = 1 << 2 // default operand size 32-bit (data segments; must be 0 when segFlagL is set)
	flatLimitHigh = 0xf    // top nibble of a 0xfffff (4 GiB, G=1) limit
)

// BuildGDT returns the 3-entry flat GDT (null, 64-bit code, data) every
// vCPU in this package's VMs uses, meant to be written at GDTAddr. The
// corresponding kvm.DTable is {Base: GDTAddr, Limit: gdtSize - 1}.
func BuildGDT() []byte {
	b := make([]byte, gdtSize)
	// b[0:8] is the null descriptor, left zero.
	encodeDescriptor(b[8:16], segTypeCode, segFlagG|segFlagL)
	encodeDescriptor(b[16:24], segTypeData, segFlagG|segFlagDB)
	return b
}

// encodeDescriptor writes a flat (base 0, limit 0xfffff with G=1, i.e. a
// full 4 GiB) segment descriptor, the layout Intel SDM vol 3 figure 3-8
// defines: a 32-bit base/20-bit limit split awkwardly across the 8 bytes
// for backward compatibility with the 80286.
func encodeDescriptor(b []byte, segType, flags byte) {
	binary.LittleEndian.PutUint16(b[0:], 0xffff) // limit 0:15
	// base 0:31 is 0, bytes [2:5] and [7] stay zero.
	b[5] = segPresent | segTypeS | segType
	b[6] = flags<<4 | flatLimitHigh
}
