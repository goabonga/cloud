// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package boot builds the byte structures a Linux kernel expects at
// entry under the x86-64 boot protocol (Documentation/x86/boot.rst in the
// kernel tree): the parsed bzImage, the "zero page" (struct boot_params)
// with its E820 memory map, the command line, GDT and identity-mapped page
// tables. It is pure — no ioctls, no file I/O beyond parsing bytes already
// read by the caller — so it can be unit tested without /dev/kvm.
package boot

// Guest-physical layout for everything this package places in low memory.
// There is no protocol requirement for these exact addresses (only that
// boot_params/GDT/page tables be readable by the vCPU once paging is on,
// and that the kernel itself load at KernelLoadAddr, which IS a protocol
// convention for non-relocatable/legacy-loaded bzImages). These values
// follow the same low-memory convention Firecracker's x86_64 loader uses,
// chosen here for the same reason: it is simple, leaves the first page
// unused (address 0 stays a fault on a stray null-pointer dereference
// instead of silently reading data), and keeps every structure well below
// 1 MiB so it is never mistaken for kernel or initrd placement.
const (
	// BootParamsAddr is where the "zero page" (struct boot_params) is
	// written; %rsi must point here at kernel entry.
	BootParamsAddr uint64 = 0x7000
	// CmdlineAddr is where the NUL-terminated kernel command line is
	// written; boot_params.hdr.cmd_line_ptr points here.
	CmdlineAddr uint64 = 0x20000
	// CmdlineMaxSize bounds how much of CmdlineAddr's page run this
	// package will use, independent of whatever the kernel's own
	// cmdline_size field allows (PlaceCmdline takes the lower of the two).
	CmdlineMaxSize = 0x10000
	// PML4Addr, PDPTAddr, PDAddr are the three page-table levels the
	// identity map in pagetable.go occupies, one 4 KiB page each.
	PML4Addr uint64 = 0x9000
	PDPTAddr uint64 = 0xa000
	PDAddr   uint64 = 0xb000
	// GDTAddr is where the flat GDT in gdt.go is written.
	GDTAddr uint64 = 0x6000
	// StackPointer is RSP at kernel entry: scratch stack space in the
	// otherwise-unused gap between GDTAddr's page and PML4Addr, enough
	// for the few instructions startup_64 executes before switching to
	// its own early stack. This is the same address Firecracker's
	// x86_64 loader uses, for the same reason (it's free in this layout
	// and known to work).
	StackPointer uint64 = 0x8ff0
	// TSSAddr and IdentityMapAddr are the addresses given to
	// kvm.VM.SetTSSAddr / SetIdentityMapAddr — conventional, widely
	// reused values (kvmtool, Firecracker, cloud-hypervisor) near the
	// top of the 32-bit address space, well above any guest memory size
	// this package's tests or the microvm resource use today, so they
	// never collide with a real memory slot.
	TSSAddr         uint64 = 0xfffbd000
	IdentityMapAddr uint64 = 0xfffbc000
	// MPTableAddr is where the Intel MP Specification table (mptable.go)
	// is written — the legacy "last 1 KiB below 640 KiB" address the
	// kernel's mpparse.c scans for it when no EBDA segment pointer is set
	// at the fixed BIOS Data Area location (0x40e), which is how it
	// always reads here since this package never writes a BDA at all.
	// This happens to be the same address BuildE820 already leaves out of
	// the RAM map as the conventional reserved EBDA/VGA/option-ROM gap —
	// writing real (if tiny) structure data into that otherwise-unused
	// region is exactly what real firmware does too.
	MPTableAddr uint64 = 0x9fc00
	// KernelLoadAddr is where the bzImage's protected-mode kernel code is
	// written. This is the historical fixed load address for a bzImage
	// not marked relocatable; a relocatable kernel (the common case on
	// modern distro kernels) is free to run from here too; it only needs
	// pref_address to be honored to run from its own preferred address,
	// which this package does not attempt, matching cloud-hypervisor's
	// own fixed-address loading (see docs/architecture/realization.md).
	KernelLoadAddr uint64 = 0x100000
)

// KernelEntryOffset is added to KernelLoadAddr to get %rip at vCPU entry:
// the Linux x86-64 boot protocol's 64-bit entry point (startup_64 in
// arch/x86/boot/compressed/head_64.S, placed there by a linker .org
// directive) is always this far into the loaded protected-mode kernel
// image, for both the 32-bit and 64-bit boot protocols — which code runs
// there depends only on the vCPU's mode at entry, which is why entering
// directly in long mode (no real-mode emulation) reaches startup_64.
const KernelEntryOffset = 0x200
