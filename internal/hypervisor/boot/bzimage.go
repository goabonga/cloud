// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package boot

import (
	"encoding/binary"
	"fmt"
)

// Byte offsets into a bzImage's boot sector / setup_header, per
// Documentation/x86/boot.rst. Every offset here is relative to the start
// of the file, which is also where struct boot_params places the embedded
// setup_header (see zeropage.go) — the two layouts are deliberately
// identical so a setup_header copied verbatim from the file lines up.
const (
	offBootFlag      = 0x1fe // __u16, must be 0xAA55
	offSetupSects    = 0x1f1 // __u8, (setup_sects+1)*512 = size of the setup code
	offHeaderMagic   = 0x202 // __u32, "HdrS" = 0x53726448
	offVersion       = 0x206 // __u16, boot protocol version, e.g. 0x020f = 2.15
	offTypeOfLoader  = 0x210 // __u8
	offLoadflags     = 0x211 // __u8
	offRamdiskImage  = 0x218 // __u32
	offRamdiskSize   = 0x21c // __u32
	offCmdLinePtr    = 0x228 // __u32
	offInitrdAddrMax = 0x22c // __u32, protocol 2.03+; 0/absent on older kernels
	offCmdlineSize   = 0x238 // __u32, protocol 2.06+; 0/absent on older kernels

	// headerStart/headerEnd bound the setup_header region boot_params
	// embeds at the same offsets (headerEnd = start of boot_params'
	// edd_mbr_sig_buffer, i.e. offset 0x290 — see zeropage.go).
	headerStart = 0x1f1
	headerEnd   = 0x290

	headerMagic = 0x53726448 // "HdrS", little-endian bytes "H","d","r","S"

	loadflagKeepSegments = 1 << 6 // bit 6, protocol 2.07+: don't reload segment registers at entry
)

// Image is a parsed bzImage: the raw setup_header bytes (to be patched and
// embedded into a zero page by BuildBootParams) and the protected-mode
// kernel code to load at KernelLoadAddr.
type Image struct {
	// Header is bytes [headerStart:headerEnd) of the source file, i.e.
	// setup_header verbatim plus boot_params' trailing padding up to the
	// E820 table — copying through the padding too is harmless and
	// simpler than tracking the header's own variable length.
	Header []byte
	// Version is the boot protocol version (e.g. 0x020f = 2.15).
	Version uint16
	// CmdlineSize is the kernel's own advertised max command-line length
	// (protocol 2.06+), or 0 if the running kernel predates that field.
	CmdlineSize uint32
	// InitrdAddrMax is the highest guest-physical address the initrd may
	// end below (protocol 2.03+), or 0 if the kernel predates that field.
	InitrdAddrMax uint32
	// Code is the protected-mode kernel image: the bytes to write at
	// KernelLoadAddr. Entry is KernelLoadAddr + KernelEntryOffset.
	Code []byte
}

// Parse validates and parses a raw bzImage file. It returns an error if the
// boot sector signature or header magic don't match, which usually means
// data isn't a bzImage (e.g. a bare vmlinux or an unrelated file) rather
// than a transient problem.
func Parse(data []byte) (*Image, error) {
	if len(data) < headerEnd {
		return nil, fmt.Errorf("boot: bzImage too short (%d bytes)", len(data))
	}
	if got := binary.LittleEndian.Uint16(data[offBootFlag:]); got != 0xaa55 {
		return nil, fmt.Errorf("boot: bad boot sector signature 0x%04x, want 0xaa55", got)
	}
	if got := binary.LittleEndian.Uint32(data[offHeaderMagic:]); got != headerMagic {
		return nil, fmt.Errorf("boot: bad setup_header magic 0x%08x, want \"HdrS\"", got)
	}

	setupSects := int(data[offSetupSects])
	if setupSects == 0 {
		setupSects = 4 // historical default when the field is 0
	}
	setupSize := (setupSects + 1) * 512
	if len(data) <= setupSize {
		return nil, fmt.Errorf("boot: bzImage has no kernel code after a %d-byte setup", setupSize)
	}

	img := &Image{
		Header:  append([]byte(nil), data[headerStart:headerEnd]...),
		Version: binary.LittleEndian.Uint16(data[offVersion:]),
		Code:    append([]byte(nil), data[setupSize:]...),
	}
	if img.Version >= 0x0203 {
		img.InitrdAddrMax = binary.LittleEndian.Uint32(data[offInitrdAddrMax:])
	}
	if img.Version >= 0x0206 {
		img.CmdlineSize = binary.LittleEndian.Uint32(data[offCmdlineSize:])
	}
	return img, nil
}

// patchForDirectBoot sets the fields a direct (non-firmware) bootloader is
// expected to fill in, in place on the header bytes: type_of_loader (an
// opaque "undefined bootloader" ID — nothing reads it back), loadflags'
// KEEP_SEGMENTS bit (we already enter with a consistent GDT/segment setup,
// see gdt.go, and don't want the kernel second-guessing it), and the
// ramdisk/cmdline pointers resolved by the caller.
func (img *Image) patchForDirectBoot(cmdlineAddr uint32, ramdiskAddr, ramdiskSize uint32) {
	const undefinedBootloader = 0xff
	img.Header[offTypeOfLoader-headerStart] = undefinedBootloader
	img.Header[offLoadflags-headerStart] |= loadflagKeepSegments
	binary.LittleEndian.PutUint32(img.Header[offCmdLinePtr-headerStart:], cmdlineAddr)
	binary.LittleEndian.PutUint32(img.Header[offRamdiskImage-headerStart:], ramdiskAddr)
	binary.LittleEndian.PutUint32(img.Header[offRamdiskSize-headerStart:], ramdiskSize)
}
