// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package boot

import (
	"encoding/binary"
	"fmt"
)

// Page-table entry flags (Intel SDM vol 3, section 4.5): present, writable,
// and — at the PD level only — PS ("page size"), marking the entry a 2 MiB
// leaf instead of a pointer to a further PT level. This package never uses
// 4 KiB pages: everything is identity-mapped with 2 MiB PD-level entries,
// the same simplification Firecracker's and crosvm's x86_64 loaders make.
const (
	pteFlagPresent = 1 << 0
	pteFlagWrite   = 1 << 1
	pdeFlagHuge    = 1 << 7

	pageSize = 0x1000
	entries  = 512 // entries per table (4096 bytes / 8-byte entries)
	twoMiB   = 2 << 20
	oneGiB   = 1 << 30
)

// BuildPageTables returns the 3-level identity-mapped page tables (PML4,
// one PDPT, and one PD per GiB of memSize, rounded up) a vCPU needs before
// entering long mode: PML4Addr, PDPTAddr and PDAddr are kept the same
// single pages layout.go documents, except PD, which occupies one 4 KiB
// page per GiB starting at PDAddr (PDAddr, PDAddr+0x1000, ...) — the
// caller (internal/hypervisor) writes each returned page at its key
// address. CR3 for KVM_SET_SREGS is PML4Addr.
//
// memSize larger than about 148 GiB would grow the PD run past
// CmdlineAddr and is rejected rather than silently overlapping it — this
// package's low-memory layout was sized for the modest VMs the microvm
// resource targets, not large hosts.
func BuildPageTables(memSize uint64) (map[uint64][]byte, error) {
	numGB := (memSize + oneGiB - 1) / oneGiB
	if numGB == 0 {
		numGB = 1
	}
	if PDAddr+numGB*pageSize > CmdlineAddr {
		return nil, fmt.Errorf("boot: %d GiB of memory needs more page-directory pages than fit below CmdlineAddr (0x%x)", numGB, CmdlineAddr)
	}

	pages := make(map[uint64][]byte, 2+numGB)

	pml4 := make([]byte, pageSize)
	binary.LittleEndian.PutUint64(pml4[0:], PDPTAddr|pteFlagPresent|pteFlagWrite)
	pages[PML4Addr] = pml4

	pdpt := make([]byte, pageSize)
	for i := uint64(0); i < numGB; i++ {
		pdAddr := PDAddr + i*pageSize
		binary.LittleEndian.PutUint64(pdpt[i*8:], pdAddr|pteFlagPresent|pteFlagWrite)
	}
	pages[PDPTAddr] = pdpt

	for i := uint64(0); i < numGB; i++ {
		pd := make([]byte, pageSize)
		for j := uint64(0); j < entries; j++ {
			addr := i*oneGiB + j*twoMiB
			binary.LittleEndian.PutUint64(pd[j*8:], addr|pteFlagPresent|pteFlagWrite|pdeFlagHuge)
		}
		pages[PDAddr+i*pageSize] = pd
	}

	return pages, nil
}
