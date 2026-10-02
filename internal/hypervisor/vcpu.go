// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package hypervisor

import (
	"fmt"

	"github.com/goabonga/infrastructure/internal/hypervisor/boot"
	"github.com/goabonga/infrastructure/internal/hypervisor/kvm"
)

// Control-register and EFER bits this package sets directly, bypassing the
// normal hardware transition sequence a real CPU goes through to reach
// long mode with paging enabled — KVM_SET_SREGS lets a VMM inject the
// end state directly, which is exactly what entering a kernel under the
// 64-bit boot protocol requires (Documentation/x86/boot.rst §4).
const (
	cr0PE = 1 << 0  // protected mode enable
	cr0ET = 1 << 4  // extension type (historically required set)
	cr0PG = 1 << 31 // paging enable

	cr4PAE = 1 << 5 // physical address extension, required for long mode

	eferLME = 1 << 8  // long mode enable
	eferLMA = 1 << 10 // long mode active — normally hardware-set, but
	// since we inject post-transition state directly rather than
	// executing the transition, we set it ourselves too; every minimal
	// x86_64 VMM that enters directly in long mode (Firecracker,
	// crosvm, cloud-hypervisor) does the same.

	rflagsReserved = 1 << 1 // bit 1 of RFLAGS is always set; IF (bit 9) stays clear: interrupts off at entry
)

// codeSegment and dataSegment are the kvm.Segment values matching the GDT
// entries boot.BuildGDT writes (CodeSelector/DataSelector) — KVM caches
// segment state in the vCPU separately from the in-memory GDT, so both
// must describe the same segment for consistency, even though only this
// cached copy is actually consulted at entry (loadflags' KEEP_SEGMENTS bit,
// set in boot.Image.patchForDirectBoot, tells the kernel not to reload
// from the GDT itself). Type values (0xb code, 0x3 data) include the
// "accessed" bit, matching the state real hardware would have left after
// actually loading these selectors — the convention every minimal VMM
// injecting segment state directly follows.
var (
	codeSegment = kvm.Segment{Base: 0, Limit: 0xfffff, Selector: boot.CodeSelector, Type: 0xb, Present: 1, S: 1, L: 1, G: 1}
	dataSegment = kvm.Segment{Base: 0, Limit: 0xfffff, Selector: boot.DataSelector, Type: 0x3, Present: 1, S: 1, DB: 1, G: 1}
)

// bootVCPU creates vCPU id, maps its kvm_run region, and sets its registers
// to the Linux x86-64 boot protocol's entry state: long mode already
// active, paging on via the identity map boot.BuildPageTables wrote,
// flat code/data segments from a GDT at boot.GDTAddr, and RSI pointing at
// the zero page loadGuest wrote at boot.BootParamsAddr.
//
// Every vCPU gets this identical configuration, id 0 (the BSP) included —
// not just id 0 as the x86-64 boot protocol alone might suggest. For any
// id other than 0, it is inert rather than a race against the BSP
// executing the same entry point: this package creates the in-kernel
// irqchip (machine.go), and per the KVM API documentation (the
// KVM_GET/SET_MP_STATE section) "this ioctl is only useful after
// KVM_CREATE_IRQCHIP[;] [w]ithout an in-kernel irqchip, the
// multiprocessing state must be maintained by userspace" — the converse,
// confirmed against Firecracker's and crosvm's own x86_64 vcpu setup
// (neither special-cases non-boot vcpus either), is that WITH one, KVM
// itself holds every non-boot vcpu in KVM_MP_STATE_UNINITIALIZED and its
// KVM_RUN calls do not execute guest code until the booted kernel's own
// SMP bring-up sends a real INIT-SIPI-SIPI over the in-kernel LAPIC —
// which resets the target vcpu's state (including %rip/%rsp) to what the
// SIPI vector specifies, discarding whatever this function configured
// here. See docs/architecture/go-hypervisor.md for the longer account.
func (m *Machine) bootVCPU(id int) (*vcpu, error) {
	kv, err := m.vm.CreateVCPU(id)
	if err != nil {
		return nil, fmt.Errorf("hypervisor: %w", err)
	}
	v := &vcpu{kv: kv}

	mmapSize, err := m.dev.VCPUMmapSize()
	if err != nil {
		return nil, fmt.Errorf("hypervisor: %w", err)
	}
	if v.run, err = kv.MapRun(mmapSize); err != nil {
		return nil, fmt.Errorf("hypervisor: %w", err)
	}

	sregs, err := kv.GetSregs()
	if err != nil {
		return nil, fmt.Errorf("hypervisor: %w", err)
	}
	sregs.CS = codeSegment
	sregs.DS, sregs.ES, sregs.FS, sregs.GS, sregs.SS = dataSegment, dataSegment, dataSegment, dataSegment, dataSegment
	sregs.GDT = kvm.DTable{Base: boot.GDTAddr, Limit: 0x17} // 3 entries * 8 bytes - 1
	sregs.CR0 = cr0PE | cr0ET | cr0PG
	sregs.CR3 = boot.PML4Addr
	sregs.CR4 = cr4PAE
	sregs.EFER = eferLME | eferLMA
	if err := kv.SetSregs(sregs); err != nil {
		return nil, fmt.Errorf("hypervisor: %w", err)
	}

	regs := kvm.Regs{
		RIP:    boot.KernelLoadAddr + boot.KernelEntryOffset,
		RSP:    boot.StackPointer,
		RSI:    boot.BootParamsAddr, // required by the 64-bit boot protocol
		RFlags: rflagsReserved,
	}
	if err := kv.SetRegs(regs); err != nil {
		return nil, fmt.Errorf("hypervisor: %w", err)
	}

	return v, nil
}
