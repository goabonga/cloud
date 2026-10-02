// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package kvm

import (
	"testing"
	"unsafe"
)

// Expected values below are hand-derived from this host's
// /usr/include/linux/kvm.h and /usr/include/x86_64-linux-gnu/asm/kvm.h
// (KVMIO=0xAE) using the _IO/_IOW/_IOR encoding in
// include/uapi/asm-generic/ioctl.h, independently of the formula this
// package's kvm.go uses to compute them — so a mistake in kvmIOCType, the
// shift constants, a struct's field order, or a struct's size is caught
// here rather than silently baked into both sides.
func TestIoctlRequestNumbers(t *testing.T) {
	cases := []struct {
		name string
		got  uintptr
		want uintptr
		cite string // linux/kvm.h or asm/kvm.h line defining the macro
	}{
		{"KVM_GET_API_VERSION", ioGetAPIVersion, 0xAE00, "linux/kvm.h:699"},
		{"KVM_CREATE_VM", ioCreateVM, 0xAE01, "linux/kvm.h:700"},
		{"KVM_CHECK_EXTENSION", ioCheckExtension, 0xAE03, "linux/kvm.h:708"},
		{"KVM_GET_VCPU_MMAP_SIZE", ioGetVCPUMmapSize, 0xAE04, "linux/kvm.h:712"},
		{"KVM_CREATE_VCPU", ioCreateVCPU, 0xAE41, "linux/kvm.h:1242"},
		{"KVM_SET_USER_MEMORY_REGION", ioSetUserMemRegion, 0x4020AE46, "linux/kvm.h:1246"},
		{"KVM_SET_TSS_ADDR", ioSetTSSAddr, 0xAE47, "linux/kvm.h:1248"},
		{"KVM_SET_IDENTITY_MAP_ADDR", ioSetIdentityMap, 0x4008AE48, "linux/kvm.h:1249"},
		{"KVM_CREATE_IRQCHIP", ioCreateIRQChip, 0xAE60, "linux/kvm.h:1260"},
		{"KVM_IRQ_LINE", ioIRQLine, 0x4008AE61, "linux/kvm.h:1261"},
		{"KVM_CREATE_PIT2", ioCreatePIT2, 0x4040AE77, "linux/kvm.h:1275"},
		{"KVM_RUN", ioRun, 0xAE80, "linux/kvm.h:1335"},
		{"KVM_GET_REGS", ioGetRegs, 0x8090AE81, "linux/kvm.h:1336"},
		{"KVM_SET_REGS", ioSetRegs, 0x4090AE82, "linux/kvm.h:1337"},
		{"KVM_GET_SREGS", ioGetSregs, 0x8138AE83, "linux/kvm.h:1338"},
		{"KVM_SET_SREGS", ioSetSregs, 0x4138AE84, "linux/kvm.h:1339"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("%s (%s): got 0x%08x, want 0x%08x", tc.name, tc.cite, tc.got, tc.want)
			}
		})
	}
}

// Struct sizes are the other half of the ioctl numbers above: a Go layout
// mistake (wrong field, wrong type, missing padding) changes the encoded
// size and so the request number, but would otherwise be an easy thing to
// get subtly wrong and not notice. Expected sizes are hand-derived from the
// same headers.
func TestStructSizes(t *testing.T) {
	cases := []struct {
		name string
		got  uintptr
		want uintptr
		cite string
	}{
		{"MemoryRegion (kvm_userspace_memory_region)", unsafe.Sizeof(MemoryRegion{}), 32, "linux/kvm.h:27"},
		{"Segment (kvm_segment)", unsafe.Sizeof(Segment{}), 24, "asm/kvm.h:132"},
		{"DTable (kvm_dtable)", unsafe.Sizeof(DTable{}), 16, "asm/kvm.h:142"},
		{"Sregs (kvm_sregs)", unsafe.Sizeof(Sregs{}), 312, "asm/kvm.h:150"},
		{"Regs (kvm_regs)", unsafe.Sizeof(Regs{}), 144, "asm/kvm.h:117"},
		{"IRQLevel (kvm_irq_level)", unsafe.Sizeof(IRQLevel{}), 8, "linux/kvm.h:58"},
		{"PITConfig (kvm_pit_config)", unsafe.Sizeof(PITConfig{}), 64, "linux/kvm.h:88"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("%s (%s): got %d bytes, want %d bytes", tc.name, tc.cite, tc.got, tc.want)
			}
		})
	}
}
