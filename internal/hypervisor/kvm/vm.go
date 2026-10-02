// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package kvm

import (
	"fmt"
	"os"
	"unsafe"
)

// VM is a handle on one KVM_CREATE_VM instance: the address space and
// in-kernel devices (irqchip, PIT) a set of vCPUs share.
type VM struct {
	f *os.File
}

// CreateVM creates a new VM (KVM_CREATE_VM). The device handle used to
// create it is not retained by the result and may be closed afterwards.
func (d *Device) CreateVM() (*VM, error) {
	fd, err := ioctl(d.Fd(), ioCreateVM, 0)
	if err != nil {
		return nil, fmt.Errorf("kvm: KVM_CREATE_VM: %w", err)
	}
	return &VM{f: os.NewFile(uintptr(fd), "kvm-vm")}, nil
}

// VCPUMmapSize returns the size, in bytes, of the shared kvm_run structure
// each vCPU's fd must be mmap'd with (KVM_GET_VCPU_MMAP_SIZE). It is a
// process-wide value, queried from the /dev/kvm handle rather than a VM.
func (d *Device) VCPUMmapSize() (int, error) {
	n, err := ioctl(d.Fd(), ioGetVCPUMmapSize, 0)
	if err != nil {
		return 0, fmt.Errorf("kvm: KVM_GET_VCPU_MMAP_SIZE: %w", err)
	}
	return n, nil
}

// Close closes the VM fd. KVM tears down every vCPU and device created from
// it when its last reference (here, this fd) closes.
func (vm *VM) Close() error {
	return vm.f.Close()
}

func (vm *VM) fd() int { return int(vm.f.Fd()) }

// SetTSSAddr reserves a 3-page region for the TSS KVM needs on Intel VMX
// hosts without unrestricted-guest support (KVM_SET_TSS_ADDR). Harmless,
// and conventionally called unconditionally, on hosts that don't need it.
// Must be called before any vCPU is created.
func (vm *VM) SetTSSAddr(addr uint64) error {
	if _, err := ioctl(vm.fd(), ioSetTSSAddr, uintptr(addr)); err != nil {
		return fmt.Errorf("kvm: KVM_SET_TSS_ADDR: %w", err)
	}
	return nil
}

// SetIdentityMapAddr reserves a one-page region for the EPT identity map
// KVM builds alongside the TSS region (KVM_SET_IDENTITY_MAP_ADDR). Must be
// called before any vCPU is created.
func (vm *VM) SetIdentityMapAddr(addr uint64) error {
	if _, err := ioctl(vm.fd(), ioSetIdentityMap, uintptr(unsafe.Pointer(&addr))); err != nil { // #nosec G103 -- KVM_SET_IDENTITY_MAP_ADDR takes a pointer to a u64 per the KVM API; no non-unsafe way to pass it through ioctl
		return fmt.Errorf("kvm: KVM_SET_IDENTITY_MAP_ADDR: %w", err)
	}
	return nil
}

// CreateIRQChip creates the in-kernel PIC, IOAPIC and per-vCPU LAPIC
// (KVM_CREATE_IRQCHIP), so KVM itself handles IPI delivery and legacy IRQ
// routing instead of this process emulating them. Must be called before any
// vCPU is created.
func (vm *VM) CreateIRQChip() error {
	if _, err := ioctl(vm.fd(), ioCreateIRQChip, 0); err != nil {
		return fmt.Errorf("kvm: KVM_CREATE_IRQCHIP: %w", err)
	}
	return nil
}

// CreatePIT2 creates the in-kernel i8254 programmable interval timer
// (KVM_CREATE_PIT2), requires CreateIRQChip to have been called first.
func (vm *VM) CreatePIT2(flags uint32) error {
	cfg := PITConfig{Flags: flags}
	if _, err := ioctl(vm.fd(), ioCreatePIT2, uintptr(unsafe.Pointer(&cfg))); err != nil { // #nosec G103 -- KVM_CREATE_PIT2 takes a pointer to struct kvm_pit_config per the KVM API; no non-unsafe way to pass it through ioctl
		return fmt.Errorf("kvm: KVM_CREATE_PIT2: %w", err)
	}
	return nil
}

// SetUserMemoryRegion registers (or, called again with the same Slot,
// replaces) a region of guest physical memory backed by host userspace
// memory the caller has already allocated (KVM_SET_USER_MEMORY_REGION) —
// typically an anonymous mmap, whose address becomes region.UserspaceAddr.
func (vm *VM) SetUserMemoryRegion(region MemoryRegion) error {
	if _, err := ioctl(vm.fd(), ioSetUserMemRegion, uintptr(unsafe.Pointer(&region))); err != nil { // #nosec G103 -- KVM_SET_USER_MEMORY_REGION takes a pointer to struct kvm_userspace_memory_region per the KVM API; no non-unsafe way to pass it through ioctl
		return fmt.Errorf("kvm: KVM_SET_USER_MEMORY_REGION(slot=%d): %w", region.Slot, err)
	}
	return nil
}

// IRQLine asserts or deasserts a GSI on the in-kernel irqchip created by
// CreateIRQChip (KVM_IRQ_LINE). level is 1 to assert, 0 to deassert; legacy
// edge-triggered ISA lines (the default routing for GSI 0-15) only need a
// single assert.
func (vm *VM) IRQLine(gsi uint32, level uint32) error {
	irq := IRQLevel{IRQ: gsi, Level: level}
	if _, err := ioctl(vm.fd(), ioIRQLine, uintptr(unsafe.Pointer(&irq))); err != nil { // #nosec G103 -- KVM_IRQ_LINE takes a pointer to struct kvm_irq_level per the KVM API; no non-unsafe way to pass it through ioctl
		return fmt.Errorf("kvm: KVM_IRQ_LINE(gsi=%d): %w", gsi, err)
	}
	return nil
}

// CreateVCPU creates vCPU number id (0 is the boot/BSP vCPU; KVM assigns it
// APIC ID id). Must be called after SetTSSAddr, SetIdentityMapAddr and
// CreateIRQChip.
func (vm *VM) CreateVCPU(id int) (*VCPU, error) {
	fd, err := ioctl(vm.fd(), ioCreateVCPU, uintptr(id))
	if err != nil {
		return nil, fmt.Errorf("kvm: KVM_CREATE_VCPU(%d): %w", id, err)
	}
	return &VCPU{id: id, f: os.NewFile(uintptr(fd), fmt.Sprintf("kvm-vcpu-%d", id))}, nil
}
