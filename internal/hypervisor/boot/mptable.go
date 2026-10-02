// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package boot

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// The structs below are hand-ported, field-for-field, from the kernel's
// Intel MP Specification 1.4 uapi header
// (arch/x86/include/asm/mpspec_def.h) — the same legacy mechanism
// Firecracker uses for x86_64 CPU topology (no ACPI/MADT at all), chosen
// here to match: it needs no RSDP/XSDT/FADT scaffolding, just this one
// self-contained, 16-byte-aligned structure a kernel without ACPI support
// compiled in (CONFIG_X86_MPPARSE, on by default on every mainstream
// distro/cloud kernel) can still enumerate CPUs from. binary.Write
// serializes each struct field-by-field in declaration order with no
// padding, independent of Go's own in-memory struct layout, so these don't
// need the kvm package's unsafe.Sizeof verification — a size mismatch here
// would only come from miscounting fields, not alignment.
type (
	mpfIntel struct {
		Signature     [4]byte
		PhysPtr       uint32
		Length        uint8
		Specification uint8
		Checksum      uint8
		Feature1      uint8
		Feature2      uint8
		Feature3      uint8
		Feature4      uint8
		Feature5      uint8
	}

	mpcTableHeader struct {
		Signature [4]byte
		Length    uint16
		Spec      uint8
		Checksum  uint8
		OEM       [8]byte
		ProductID [12]byte
		OEMPtr    uint32
		OEMSize   uint16
		OEMCount  uint16
		LAPIC     uint32
		Reserved  uint32
	}

	mpcCPU struct {
		Type        uint8
		APICID      uint8
		APICVer     uint8
		CPUFlag     uint8
		CPUFeature  uint32
		FeatureFlag uint32
		Reserved    [2]uint32
	}

	mpcBus struct {
		Type    uint8
		BusID   uint8
		BusType [6]byte
	}

	mpcIOAPIC struct {
		Type     uint8
		APICID   uint8
		APICVer  uint8
		Flags    uint8
		APICAddr uint32
	}

	mpcLintsrc struct {
		Type         uint8
		IRQType      uint8
		IRQFlag      uint16
		SrcBusID     uint8
		SrcBusIRQ    uint8
		DestAPIC     uint8
		DestAPICLint uint8
	}
)

// Entry type bytes (MP_*), CPU flags, IRQ source types and the other fixed
// field values the Linux kernel's mpparse.c expects — all from
// arch/x86/include/asm/mpspec_def.h.
const (
	mpEntryProcessor = 0
	mpEntryBus       = 1
	mpEntryIOAPIC    = 2
	mpEntryLintsrc   = 4

	mpCPUEnabled       = 1 << 0
	mpCPUBootProcessor = 1 << 1

	mpIRQTypeNMI    = 1
	mpIRQTypeExtINT = 3

	mpcAPICUsable = 1

	mpAPICVersion = 0x14 // the version byte every minimal x86_64 VMM reports (Firecracker, kvmtool)

	mpLAPICDefaultBase  = 0xfee00000
	mpIOAPICDefaultBase = 0xfec00000

	// maxMPCPUs is the Intel MP Spec's own limit: an 8-bit APIC ID field
	// with one value reserved for the IOAPIC.
	maxMPCPUs = 254
)

// BuildMPTable returns the Intel MP floating pointer and configuration
// table (one CPU entry per numCPUs, one ISA bus, one IOAPIC, and the two
// local-interrupt entries wiring LINT0 to the legacy 8259 PIC's ExtINT and
// LINT1 to NMI — the same minimal, proven set Firecracker emits), meant to
// be written at MPTableAddr. numCPUs must be between 1 and 254.
func BuildMPTable(numCPUs int) ([]byte, error) {
	if numCPUs < 1 || numCPUs > maxMPCPUs {
		return nil, fmt.Errorf("boot: %d cpus, must be between 1 and %d", numCPUs, maxMPCPUs)
	}

	var cfg bytes.Buffer
	mustWrite(&cfg, mpcTableHeader{}) // placeholder, filled in and rewritten below once Length/Checksum are known

	for id := range numCPUs {
		flag := uint8(mpCPUEnabled)
		if id == 0 {
			flag |= mpCPUBootProcessor
		}
		mustWrite(&cfg, mpcCPU{
			Type:        mpEntryProcessor,
			APICID:      uint8(id),
			APICVer:     mpAPICVersion,
			CPUFlag:     flag,
			CPUFeature:  0x600, // family/model/stepping: a placeholder value, never read back by the kernel's own enumeration
			FeatureFlag: 0x201, // CPUID feature bits: FPU | APIC-on-chip, the two the kernel's MP-table path checks for
		})
	}

	ioapicID := uint8(numCPUs)
	mustWrite(&cfg, mpcBus{Type: mpEntryBus, BusID: 0, BusType: [6]byte{'I', 'S', 'A', ' ', ' ', ' '}})
	mustWrite(&cfg, mpcIOAPIC{Type: mpEntryIOAPIC, APICID: ioapicID, APICVer: mpAPICVersion, Flags: mpcAPICUsable, APICAddr: mpIOAPICDefaultBase})
	mustWrite(&cfg, mpcLintsrc{Type: mpEntryLintsrc, IRQType: mpIRQTypeExtINT, SrcBusID: 0, DestAPIC: mpAPICAll, DestAPICLint: 0})
	mustWrite(&cfg, mpcLintsrc{Type: mpEntryLintsrc, IRQType: mpIRQTypeNMI, SrcBusID: 0, DestAPIC: mpAPICAll, DestAPICLint: 1})

	header := mpcTableHeader{
		Signature: [4]byte{'P', 'C', 'M', 'P'},
		Length:    uint16(cfg.Len()), // #nosec G115 -- cfg holds at most maxMPCPUs fixed-size entries plus a handful of fixed ones, nowhere near uint16's range
		Spec:      4,
		OEM:       [8]byte{'G', 'O', 'A', ' ', ' ', ' ', ' ', ' '},
		ProductID: [12]byte{'g', 'o', '-', 'h', 'y', 'p', 'e', 'r', 'v', 'i', 's', 'r'},
		OEMCount:  uint16(numCPUs) + 1 + 1 + 2, // cpus + bus + ioapic + 2 lintsrc
		LAPIC:     mpLAPICDefaultBase,
	}
	cfgBytes := cfg.Bytes()
	headerBytes := marshal(header)
	copy(cfgBytes[:len(headerBytes)], headerBytes) // overwrite the placeholder now Length is known
	cfgBytes[7] = checksum8(cfgBytes)              // offset 7: Checksum (Signature[4], Length u16, Spec u8, then Checksum)

	fp := mpfIntel{
		Signature:     [4]byte{'_', 'M', 'P', '_'},
		Length:        1, // in 16-byte units: this structure is always exactly 16 bytes
		Specification: 4,
	}
	fpBytes := marshal(fp)

	var out bytes.Buffer
	out.Write(fpBytes)
	out.Write(cfgBytes)
	result := out.Bytes()

	// PhysPtr (offset 4, 4 bytes) must point at the config table, which
	// immediately follows the floating pointer in this layout.
	binary.LittleEndian.PutUint32(result[4:], uint32(MPTableAddr)+uint32(len(fpBytes))) // #nosec G115 -- MPTableAddr is a fixed low-memory constant and fpBytes is always exactly 16 bytes (the floating pointer structure)
	result[10] = checksum8(result[:16])                                                 // offset 10: Checksum (Signature[4], PhysPtr u32, Length u8, Specification u8, then Checksum), covering only the 16-byte floating pointer itself

	return result, nil
}

// mpAPICAll (0xff) in DestAPIC means "all local APICs", the standard way
// to route a local-interrupt entry when every CPU's LINT pins are wired
// identically — true here since every vCPU is configured the same way.
const mpAPICAll = 0xff

func mustWrite(buf *bytes.Buffer, v any) {
	if err := binary.Write(buf, binary.LittleEndian, v); err != nil {
		// Every struct here is a fixed-size value binary.Write supports
		// unconditionally; a failure here means a struct field stopped
		// being one, a programming error no caller can recover from.
		panic(fmt.Sprintf("boot: marshal %T: %v", v, err))
	}
}

func marshal(v any) []byte {
	var buf bytes.Buffer
	mustWrite(&buf, v)
	return buf.Bytes()
}

// checksum8 returns the byte that makes b's bytes sum to 0 mod 256 — the
// standard BIOS-table checksum algorithm, computed with the checksum
// field's own byte left at 0 in b.
func checksum8(b []byte) byte {
	var sum byte
	for _, c := range b {
		sum += c
	}
	return -sum
}
