// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package kvm

// The structs below are hand-ported, field-for-field, from the kernel's
// x86-64 KVM uapi headers (/usr/include/linux/kvm.h and
// /usr/include/x86_64-linux-gnu/asm/kvm.h) so their Go layout and
// unsafe.Sizeof match the C ABI exactly: same field order, same fixed-width
// integer types, and the same explicit padding fields the kernel headers
// use (Go does not otherwise guarantee identical layout to C). Each type's
// doc comment cites the struct it mirrors.

// MemoryRegion mirrors struct kvm_userspace_memory_region, the argument to
// KVM_SET_USER_MEMORY_REGION.
type MemoryRegion struct {
	Slot          uint32
	Flags         uint32
	GuestPhysAddr uint64
	MemorySize    uint64
	UserspaceAddr uint64
}

// Segment mirrors struct kvm_segment, used embedded in Sregs.
type Segment struct {
	Base     uint64
	Limit    uint32
	Selector uint16
	Type     uint8
	Present  uint8
	DPL      uint8
	DB       uint8
	S        uint8
	L        uint8
	G        uint8
	AVL      uint8
	Unusable uint8
	Padding  uint8
}

// DTable mirrors struct kvm_dtable (GDTR/IDTR), used embedded in Sregs.
type DTable struct {
	Base    uint64
	Limit   uint16
	Padding [3]uint16
}

// kvmNRInterrupts is KVM_NR_INTERRUPTS (asm/kvm.h), sizing Sregs'
// InterruptBitmap.
const kvmNRInterrupts = 256

// Sregs mirrors struct kvm_sregs, the argument to KVM_GET_SREGS /
// KVM_SET_SREGS.
type Sregs struct {
	CS, DS, ES, FS, GS, SS  Segment
	TR, LDT                 Segment
	GDT, IDT                DTable
	CR0, CR2, CR3, CR4, CR8 uint64
	EFER                    uint64
	ApicBase                uint64
	InterruptBitmap         [(kvmNRInterrupts + 63) / 64]uint64
}

// Regs mirrors struct kvm_regs, the argument to KVM_GET_REGS / KVM_SET_REGS.
type Regs struct {
	RAX, RBX, RCX, RDX uint64
	RSI, RDI, RSP, RBP uint64
	R8, R9, R10, R11   uint64
	R12, R13, R14, R15 uint64
	RIP, RFlags        uint64
}

// IRQLevel mirrors struct kvm_irq_level, the argument to KVM_IRQ_LINE.
type IRQLevel struct {
	IRQ   uint32 // GSI, not the legacy PIC line number
	Level uint32
}

// PITConfig mirrors struct kvm_pit_config, the argument to KVM_CREATE_PIT2.
type PITConfig struct {
	Flags uint32
	_     [15]uint32
}
