// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package kvm

import (
	"fmt"
	"os"
	"unsafe"
)

// VCPU is a handle on one KVM_CREATE_VCPU instance. Every ioctl on it,
// including Run (added once the run loop lands), must be issued from the
// same OS thread for the lifetime of the vCPU — KVM tracks ioctl state
// per-thread, and Go's scheduler is otherwise free to migrate a goroutine
// across threads between syscalls.
type VCPU struct {
	id int
	f  *os.File
}

// ID returns the vCPU number it was created with (CreateVCPU's id).
func (v *VCPU) ID() int { return v.id }

// Close closes the vCPU fd.
func (v *VCPU) Close() error {
	return v.f.Close()
}

func (v *VCPU) fd() int { return int(v.f.Fd()) }

// GetRegs reads the vCPU's general-purpose registers (KVM_GET_REGS).
func (v *VCPU) GetRegs() (Regs, error) {
	var regs Regs
	if _, err := ioctl(v.fd(), ioGetRegs, uintptr(unsafe.Pointer(&regs))); err != nil {
		return Regs{}, fmt.Errorf("kvm: KVM_GET_REGS: %w", err)
	}
	return regs, nil
}

// SetRegs writes the vCPU's general-purpose registers (KVM_SET_REGS).
func (v *VCPU) SetRegs(regs Regs) error {
	if _, err := ioctl(v.fd(), ioSetRegs, uintptr(unsafe.Pointer(&regs))); err != nil {
		return fmt.Errorf("kvm: KVM_SET_REGS: %w", err)
	}
	return nil
}

// GetSregs reads the vCPU's special registers: segments, control registers,
// EFER (KVM_GET_SREGS).
func (v *VCPU) GetSregs() (Sregs, error) {
	var sregs Sregs
	if _, err := ioctl(v.fd(), ioGetSregs, uintptr(unsafe.Pointer(&sregs))); err != nil {
		return Sregs{}, fmt.Errorf("kvm: KVM_GET_SREGS: %w", err)
	}
	return sregs, nil
}

// SetSregs writes the vCPU's special registers (KVM_SET_SREGS).
func (v *VCPU) SetSregs(sregs Sregs) error {
	if _, err := ioctl(v.fd(), ioSetSregs, uintptr(unsafe.Pointer(&sregs))); err != nil {
		return fmt.Errorf("kvm: KVM_SET_SREGS: %w", err)
	}
	return nil
}
