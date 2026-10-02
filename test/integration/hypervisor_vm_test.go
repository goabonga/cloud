// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

//go:build integration

package integration

import (
	"errors"
	"io/fs"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/goabonga/infrastructure/internal/hypervisor/kvm"
)

// TestKVMCreateVMAndVCPU exercises the VM/vCPU/memory-region lifecycle
// wrappers against a real host: create a VM, configure the Intel-VMX
// TSS/identity-map workaround, the in-kernel irqchip and PIT, register a
// page of guest memory, create one vCPU and read its (KVM-default) initial
// registers back. It does not boot anything — that needs the boot-protocol
// package, added in a later milestone.
func TestKVMCreateVMAndVCPU(t *testing.T) {
	dev, err := kvm.Open()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
			t.Skipf("%s not usable: %v", kvm.DevicePath, err)
		}
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = dev.Close() }()

	vm, err := dev.CreateVM()
	if err != nil {
		t.Fatalf("CreateVM: %v", err)
	}
	defer func() { _ = vm.Close() }()

	if err := vm.SetTSSAddr(0xfffbd000); err != nil {
		t.Fatalf("SetTSSAddr: %v", err)
	}
	if err := vm.SetIdentityMapAddr(0xfffbc000); err != nil {
		t.Fatalf("SetIdentityMapAddr: %v", err)
	}
	if err := vm.CreateIRQChip(); err != nil {
		t.Fatalf("CreateIRQChip: %v", err)
	}
	if err := vm.CreatePIT2(0); err != nil {
		t.Fatalf("CreatePIT2: %v", err)
	}

	const memSize = 1 << 20 // one page suffices for this smoke test
	mem, err := unix.Mmap(-1, 0, memSize, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED|unix.MAP_ANONYMOUS)
	if err != nil {
		t.Fatalf("mmap guest memory: %v", err)
	}
	defer func() { _ = unix.Munmap(mem) }()

	if err := vm.SetUserMemoryRegion(kvm.MemoryRegion{
		Slot:          0,
		GuestPhysAddr: 0,
		MemorySize:    memSize,
		UserspaceAddr: uint64(uintptr(unsafe.Pointer(&mem[0]))),
	}); err != nil {
		t.Fatalf("SetUserMemoryRegion: %v", err)
	}

	vcpu, err := vm.CreateVCPU(0)
	if err != nil {
		t.Fatalf("CreateVCPU: %v", err)
	}
	defer func() { _ = vcpu.Close() }()

	if vcpu.ID() != 0 {
		t.Fatalf("vcpu.ID() = %d, want 0", vcpu.ID())
	}

	if _, err := vcpu.GetRegs(); err != nil {
		t.Fatalf("GetRegs: %v", err)
	}
	if _, err := vcpu.GetSregs(); err != nil {
		t.Fatalf("GetSregs: %v", err)
	}
}
