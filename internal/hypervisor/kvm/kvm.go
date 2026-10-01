// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package kvm wraps the raw /dev/kvm ioctl interface: opening the device,
// encoding the ioctl request numbers and the uapi structs KVM's x86-64 API
// expects. It has no policy of its own (no boot protocol, no device
// emulation) — internal/hypervisor builds a VM out of these primitives.
package kvm

import (
	"fmt"
	"os"
	"unsafe"
)

// DevicePath is the device path KVM exposes on a host with the
// kvm/kvm_intel (or kvm_amd) kernel modules loaded.
const DevicePath = "/dev/kvm"

// Ioctl request numbers for the subset of the KVM API this package covers,
// encoded per include/uapi/asm-generic/ioctl.h from kvmIOCType and each
// struct's real size (via unsafe.Sizeof on the Go mirror types in
// types.go, so a struct-layout mistake there breaks the encoded number too,
// not just the argument marshaling). kvm_test.go checks every one of these
// against a value hand-derived from /usr/include/linux/kvm.h.
var (
	ioGetAPIVersion    = io(0x00)
	ioCreateVM         = io(0x01)
	ioCheckExtension   = io(0x03)
	ioGetVCPUMmapSize  = io(0x04)
	ioCreateVCPU       = io(0x41)
	ioSetUserMemRegion = iow(0x46, unsafe.Sizeof(MemoryRegion{}))
	ioSetTSSAddr       = io(0x47)
	ioSetIdentityMap   = iow(0x48, unsafe.Sizeof(uint64(0)))
	ioCreateIRQChip    = io(0x60)
	ioIRQLine          = iow(0x61, unsafe.Sizeof(IRQLevel{}))
	ioCreatePIT2       = iow(0x77, unsafe.Sizeof(PITConfig{}))
	ioRun              = io(0x80)
	ioGetRegs          = ior(0x81, unsafe.Sizeof(Regs{}))
	ioSetRegs          = iow(0x82, unsafe.Sizeof(Regs{}))
	ioGetSregs         = ior(0x83, unsafe.Sizeof(Sregs{}))
	ioSetSregs         = iow(0x84, unsafe.Sizeof(Sregs{}))
)

// Device is an open handle on /dev/kvm, the entry point for querying the
// host's KVM API before creating any VM.
type Device struct {
	f *os.File
}

// Open opens /dev/kvm. It requires read/write access to the device (root,
// or membership in the kvm group).
func Open() (*Device, error) {
	f, err := os.OpenFile(DevicePath, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("kvm: open %s: %w", DevicePath, err)
	}
	return &Device{f: f}, nil
}

// Close closes the device handle. It does not affect VMs already created
// from it.
func (d *Device) Close() error {
	return d.f.Close()
}

// Fd returns the underlying file descriptor, for ioctls issued by
// internal/hypervisor against VM/vCPU fds obtained through this device.
func (d *Device) Fd() int {
	return int(d.f.Fd())
}

// APIVersion returns the host's KVM API version (KVM_GET_API_VERSION). A
// value other than 12 means this package's assumptions about the API no
// longer hold.
func (d *Device) APIVersion() (int, error) {
	v, err := ioctl(d.Fd(), ioGetAPIVersion, 0)
	if err != nil {
		return 0, fmt.Errorf("kvm: KVM_GET_API_VERSION: %w", err)
	}
	return v, nil
}

// CheckExtension reports the host's support level for a KVM_CAP_*
// extension (KVM_CHECK_EXTENSION): 0 means unsupported, a positive value's
// meaning is capability-specific (often just "supported").
func (d *Device) CheckExtension(cap int) (int, error) {
	v, err := ioctl(d.Fd(), ioCheckExtension, uintptr(cap))
	if err != nil {
		return 0, fmt.Errorf("kvm: KVM_CHECK_EXTENSION(%d): %w", cap, err)
	}
	return v, nil
}
