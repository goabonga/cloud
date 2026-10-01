// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package boot_test

import (
	"encoding/binary"
	"testing"

	"github.com/goabonga/infrastructure/internal/hypervisor/boot"
)

// sumBytesMod256 is the verification side of the standard BIOS-table
// checksum algorithm: a correctly checksummed region always sums to 0.
func sumBytesMod256(b []byte) byte {
	var sum byte
	for _, c := range b {
		sum += c
	}
	return sum
}

func TestBuildMPTableFloatingPointer(t *testing.T) {
	table, err := boot.BuildMPTable(2)
	if err != nil {
		t.Fatalf("BuildMPTable: %v", err)
	}
	if len(table) < 16 {
		t.Fatalf("len(table) = %d, shorter than the 16-byte floating pointer alone", len(table))
	}

	fp := table[:16]
	if string(fp[0:4]) != "_MP_" {
		t.Errorf("floating pointer signature = %q, want \"_MP_\"", fp[0:4])
	}
	if got := binary.LittleEndian.Uint32(fp[4:8]); got != uint32(boot.MPTableAddr)+16 {
		t.Errorf("PhysPtr = 0x%x, want 0x%x (MPTableAddr + 16, the config table immediately follows)", got, uint32(boot.MPTableAddr)+16)
	}
	if fp[8] != 1 {
		t.Errorf("Length = %d, want 1 (16-byte units)", fp[8])
	}
	if fp[9] != 4 {
		t.Errorf("Specification = %d, want 4 (MP Spec 1.4)", fp[9])
	}
	if sum := sumBytesMod256(fp); sum != 0 {
		t.Errorf("floating pointer bytes sum to %d mod 256, want 0 (bad checksum)", sum)
	}
}

func TestBuildMPTableConfigHeader(t *testing.T) {
	const numCPUs = 3
	table, err := boot.BuildMPTable(numCPUs)
	if err != nil {
		t.Fatalf("BuildMPTable: %v", err)
	}

	cfg := table[16:]
	if string(cfg[0:4]) != "PCMP" {
		t.Errorf("config table signature = %q, want \"PCMP\"", cfg[0:4])
	}
	wantLength := uint16(len(cfg))
	if got := binary.LittleEndian.Uint16(cfg[4:6]); got != wantLength {
		t.Errorf("Length = %d, want %d (the whole config table, header included)", got, wantLength)
	}
	if cfg[6] != 4 {
		t.Errorf("Spec = %d, want 4", cfg[6])
	}
	wantOEMCount := uint16(numCPUs + 1 + 1 + 2) // cpus + bus + ioapic + 2 lintsrc
	if got := binary.LittleEndian.Uint16(cfg[34:36]); got != wantOEMCount {
		t.Errorf("OEMCount = %d, want %d", got, wantOEMCount)
	}
	if got := binary.LittleEndian.Uint32(cfg[36:40]); got != 0xfee00000 {
		t.Errorf("LAPIC base = 0x%x, want 0xfee00000", got)
	}
	if sum := sumBytesMod256(cfg); sum != 0 {
		t.Errorf("config table bytes sum to %d mod 256, want 0 (bad checksum)", sum)
	}
}

func TestBuildMPTableCPUEntries(t *testing.T) {
	const numCPUs = 4
	table, err := boot.BuildMPTable(numCPUs)
	if err != nil {
		t.Fatalf("BuildMPTable: %v", err)
	}

	const headerSize = 44
	entries := table[16+headerSize:]
	for id := 0; id < numCPUs; id++ {
		off := id * 20 // mpc_cpu is 20 bytes
		entry := entries[off : off+20]
		if entry[0] != 0 { // Type = MP_PROCESSOR
			t.Fatalf("cpu %d: Type = %d, want 0 (MP_PROCESSOR)", id, entry[0])
		}
		if entry[1] != byte(id) { // APICID
			t.Errorf("cpu %d: APICID = %d, want %d", id, entry[1], id)
		}
		wantFlag := byte(1) // CPU_ENABLED
		if id == 0 {
			wantFlag |= 1 << 1 // CPU_BOOTPROCESSOR
		}
		if entry[3] != wantFlag { // CPUFlag
			t.Errorf("cpu %d: CPUFlag = 0x%x, want 0x%x", id, entry[3], wantFlag)
		}
	}

	// The IOAPIC entry (bus entry first, then ioapic) gets the next free
	// APIC ID after the CPUs.
	busOff := 16 + headerSize + numCPUs*20
	if table[busOff] != 1 { // Type = MP_BUS
		t.Fatalf("bus entry Type = %d, want 1 (MP_BUS)", table[busOff])
	}
	ioapicOff := busOff + 8    // mpc_bus is 8 bytes
	if table[ioapicOff] != 2 { // Type = MP_IOAPIC
		t.Fatalf("ioapic entry Type = %d, want 2 (MP_IOAPIC)", table[ioapicOff])
	}
	if got := table[ioapicOff+1]; got != byte(numCPUs) {
		t.Errorf("ioapic APICID = %d, want %d (first free id after the cpus)", got, numCPUs)
	}
	if got := binary.LittleEndian.Uint32(table[ioapicOff+4 : ioapicOff+8]); got != 0xfec00000 {
		t.Errorf("ioapic APICAddr = 0x%x, want 0xfec00000", got)
	}
}

func TestBuildMPTableRejectsOutOfRangeCPUCount(t *testing.T) {
	if _, err := boot.BuildMPTable(0); err == nil {
		t.Error("BuildMPTable(0) succeeded, want error")
	}
	if _, err := boot.BuildMPTable(255); err == nil {
		t.Error("BuildMPTable(255) succeeded, want error (254 is the Intel MP Spec's own cap)")
	}
	if _, err := boot.BuildMPTable(254); err != nil {
		t.Errorf("BuildMPTable(254) = %v, want success (254 is the cap, not past it)", err)
	}
}
