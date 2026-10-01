// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package hypervisor assembles internal/hypervisor/kvm's raw ioctl
// wrappers and internal/hypervisor/boot's pure byte builders into a
// bootable VM: it owns guest memory, decides where everything boot.go
// builds gets written, and drives the vCPU run loop. cmd/hypervisor is the
// thin binary wrapping this package with a control socket.
package hypervisor

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/goabonga/infrastructure/internal/hypervisor/boot"
	"github.com/goabonga/infrastructure/internal/hypervisor/kvm"
)

// Config describes the VM to boot. VCPUs must be 1 for now — multi-vCPU
// lands in a later milestone.
type Config struct {
	VCPUs      int
	MemoryMB   int
	KernelPath string
	InitrdPath string
	CmdLine    string
}

// Machine is one booted (or about to be booted) VM: its KVM handles, its
// guest memory, and its vCPUs.
type Machine struct {
	dev *kvm.Device
	vm  *kvm.VM
	mem []byte // guest-physical address 0 maps to mem[0]

	vcpus []*vcpu
}

type vcpu struct {
	kv  *kvm.VCPU
	run *kvm.Run
}

// New assembles a Machine from cfg: opens /dev/kvm, creates the VM and its
// vCPU(s), allocates and populates guest memory (kernel, initrd, cmdline,
// page tables, GDT, boot_params) per the x86-64 Linux boot protocol, and
// configures the boot vCPU's registers to enter it — everything Run needs
// to actually start executing the guest.
func New(cfg Config) (*Machine, error) {
	if cfg.VCPUs != 1 {
		return nil, fmt.Errorf("hypervisor: %d vcpus requested, only 1 is supported for now", cfg.VCPUs)
	}
	if cfg.MemoryMB <= 0 {
		return nil, fmt.Errorf("hypervisor: memory must be positive, got %d MiB", cfg.MemoryMB)
	}

	kernelData, err := os.ReadFile(cfg.KernelPath)
	if err != nil {
		return nil, fmt.Errorf("hypervisor: read kernel: %w", err)
	}
	img, err := boot.Parse(kernelData)
	if err != nil {
		return nil, fmt.Errorf("hypervisor: %w", err)
	}

	var initrdData []byte
	if cfg.InitrdPath != "" {
		initrdData, err = os.ReadFile(cfg.InitrdPath)
		if err != nil {
			return nil, fmt.Errorf("hypervisor: read initrd: %w", err)
		}
	}

	m := &Machine{}
	ok := false
	defer func() {
		if !ok {
			_ = m.Close()
		}
	}()

	memSize := uint64(cfg.MemoryMB) * 1024 * 1024

	if m.dev, err = kvm.Open(); err != nil {
		return nil, fmt.Errorf("hypervisor: %w", err)
	}
	if m.vm, err = m.dev.CreateVM(); err != nil {
		return nil, fmt.Errorf("hypervisor: %w", err)
	}
	if err := m.vm.SetTSSAddr(boot.TSSAddr); err != nil {
		return nil, fmt.Errorf("hypervisor: %w", err)
	}
	if err := m.vm.SetIdentityMapAddr(boot.IdentityMapAddr); err != nil {
		return nil, fmt.Errorf("hypervisor: %w", err)
	}
	if err := m.vm.CreateIRQChip(); err != nil {
		return nil, fmt.Errorf("hypervisor: %w", err)
	}
	if err := m.vm.CreatePIT2(0); err != nil {
		return nil, fmt.Errorf("hypervisor: %w", err)
	}

	m.mem, err = unix.Mmap(-1, 0, int(memSize), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED|unix.MAP_ANONYMOUS)
	if err != nil {
		return nil, fmt.Errorf("hypervisor: mmap %d bytes of guest memory: %w", memSize, err)
	}
	if err := m.vm.SetUserMemoryRegion(kvm.MemoryRegion{
		Slot:          0,
		GuestPhysAddr: 0,
		MemorySize:    memSize,
		UserspaceAddr: uint64(uintptr(unsafe.Pointer(&m.mem[0]))),
	}); err != nil {
		return nil, fmt.Errorf("hypervisor: %w", err)
	}

	if err := m.loadGuest(img, initrdData, cfg.CmdLine, memSize); err != nil {
		return nil, err
	}

	v, err := m.bootVCPU()
	if err != nil {
		return nil, err
	}
	m.vcpus = []*vcpu{v}

	ok = true
	return m, nil
}

// write copies b into guest memory at guest-physical address addr,
// erroring rather than panicking if it would run past the end of memory —
// a sign of a boot-protocol layout bug (e.g. too little memory for the
// kernel/initrd/cmdline placement boot package computed), not something
// that should take the process down.
func (m *Machine) write(addr uint64, b []byte) error {
	if addr+uint64(len(b)) > uint64(len(m.mem)) {
		return fmt.Errorf("hypervisor: write of %d bytes at 0x%x overruns %d bytes of guest memory", len(b), addr, len(m.mem))
	}
	copy(m.mem[addr:], b)
	return nil
}

// loadGuest writes every structure the boot protocol needs into guest
// memory: page tables, GDT, kernel, initrd, command line and the zero
// page tying them together.
func (m *Machine) loadGuest(img *boot.Image, initrdData []byte, cmdline string, memSize uint64) error {
	pages, err := boot.BuildPageTables(memSize)
	if err != nil {
		return fmt.Errorf("hypervisor: %w", err)
	}
	for addr, page := range pages {
		if err := m.write(addr, page); err != nil {
			return err
		}
	}

	if err := m.write(boot.GDTAddr, boot.BuildGDT()); err != nil {
		return err
	}

	cmdlineBytes, err := boot.BuildCmdline(cmdline, img.CmdlineSize)
	if err != nil {
		return fmt.Errorf("hypervisor: %w", err)
	}
	if err := m.write(boot.CmdlineAddr, cmdlineBytes); err != nil {
		return err
	}

	initrdAddr, err := boot.PlaceInitrd(memSize, len(initrdData), img.InitrdAddrMax)
	if err != nil {
		return fmt.Errorf("hypervisor: %w", err)
	}
	if len(initrdData) > 0 {
		if err := m.write(initrdAddr, initrdData); err != nil {
			return err
		}
	}

	e820, err := boot.BuildE820(memSize)
	if err != nil {
		return fmt.Errorf("hypervisor: %w", err)
	}
	zp, err := img.BuildBootParams(uint32(boot.CmdlineAddr), uint32(initrdAddr), uint32(len(initrdData)), e820)
	if err != nil {
		return fmt.Errorf("hypervisor: %w", err)
	}
	if err := m.write(boot.BootParamsAddr, zp); err != nil {
		return err
	}

	return m.write(boot.KernelLoadAddr, img.Code)
}

// Close tears the VM down: unmaps every vCPU's kvm_run region, closes every
// fd, and unmaps guest memory. It is safe to call on a partially
// constructed Machine (New calls it itself on any setup failure) and more
// than once.
func (m *Machine) Close() error {
	for _, v := range m.vcpus {
		if v.run != nil {
			_ = v.run.Unmap()
		}
		if v.kv != nil {
			_ = v.kv.Close()
		}
	}
	m.vcpus = nil
	if m.mem != nil {
		_ = unix.Munmap(m.mem)
		m.mem = nil
	}
	if m.vm != nil {
		_ = m.vm.Close()
		m.vm = nil
	}
	if m.dev != nil {
		_ = m.dev.Close()
		m.dev = nil
	}
	return nil
}
