// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package kvm

import (
	"encoding/binary"
	"fmt"

	"golang.org/x/sys/unix"
)

// Exit reasons (KVM_EXIT_*, linux/kvm.h) this package's callers dispatch
// on. Unlisted reasons exist in the kernel API but aren't needed by a
// minimal direct-kernel-boot VMM with an in-kernel irqchip and PIT.
const (
	ExitUnknown       = 0
	ExitIO            = 2
	ExitHLT           = 5
	ExitMMIO          = 6
	ExitShutdown      = 8
	ExitFailEntry     = 9
	ExitInternalError = 17
)

// PIO directions (KVM_EXIT_IO_IN/OUT), as seen from the guest: IN reads a
// port (the device must fill Run's IO data before returning from Run),
// OUT writes one (the device reads it from Run's IO data).
const (
	IODirIn  = 0
	IODirOut = 1
)

// Byte offsets into struct kvm_run (linux/kvm.h) this package reads or
// writes directly against the mmap'd region, rather than redeclaring the
// whole (deeply unionized, architecture-dependent) struct in Go.
const (
	runExitReason = 0x08 // __u32

	// The exit-specific union starts here; each exit reason's payload is
	// a different interpretation of the same bytes.
	runUnion = 0x20

	runIODirection  = runUnion + 0x00 // __u8
	runIOSize       = runUnion + 0x01 // __u8
	runIOPort       = runUnion + 0x02 // __u16
	runIOCount      = runUnion + 0x04 // __u32
	runIODataOffset = runUnion + 0x08 // __u64, relative to the start of kvm_run

	runMMIOPhysAddr = runUnion + 0x00 // __u64
	runMMIOData     = runUnion + 0x08 // __u8[8]
	runMMIOLen      = runUnion + 0x10 // __u32
	runMMIOIsWrite  = runUnion + 0x14 // __u8

	runFailEntryReason = runUnion + 0x00 // __u64
	runFailEntryCPU    = runUnion + 0x08 // __u32

	runInternalSuberror = runUnion + 0x00 // __u32
	runInternalNdata    = runUnion + 0x04 // __u32
)

// Run is the kvm_run structure KVM shares with userspace by mmap'ing a
// vCPU's fd (KVM_GET_VCPU_MMAP_SIZE bytes); KVM_RUN reads vCPU input from
// it (for KVM_EXIT_IO reads) and fills it with exit details on return.
type Run struct {
	b []byte
}

// MapRun mmaps this vCPU's kvm_run region. size must be the value Device's
// VCPUMmapSize returned; it is process-wide, not per-vCPU, so it is the
// caller's to cache and pass in rather than this package re-querying it.
func (v *VCPU) MapRun(size int) (*Run, error) {
	b, err := unix.Mmap(v.fd(), 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("kvm: mmap vcpu %d run struct: %w", v.id, err)
	}
	return &Run{b: b}, nil
}

// Unmap releases the mmap'd region. The vCPU itself is unaffected; close
// it separately via VCPU.Close.
func (r *Run) Unmap() error {
	return unix.Munmap(r.b)
}

// Run issues KVM_RUN: the vCPU executes until the next exit (an I/O or
// MMIO access this process must handle, a halt, a shutdown condition, or
// an error) or until Run is interrupted by a signal, which surfaces as
// EINTR here — a normal condition for callers that signal a run loop to
// stop, not a real failure.
func (v *VCPU) Run() error {
	if _, err := ioctl(v.fd(), ioRun, 0); err != nil {
		return fmt.Errorf("kvm: KVM_RUN: %w", err)
	}
	return nil
}

// ExitReason returns why the preceding Run returned (KVM_EXIT_*).
func (r *Run) ExitReason() uint32 {
	return binary.LittleEndian.Uint32(r.b[runExitReason:])
}

// IO returns the KVM_EXIT_IO payload: the port I/O access to handle.
// Direction is IODirIn or IODirOut, size is 1/2/4 bytes per access, and
// Data holds count*size bytes (a string IN/OUT can repeat the access
// count times) — for IODirIn, the device must fill Data before the next
// Run call; for IODirOut, Data holds what the guest wrote.
func (r *Run) IO() (direction, size uint8, port uint16, data []byte) {
	direction = r.b[runIODirection]
	size = r.b[runIOSize]
	port = binary.LittleEndian.Uint16(r.b[runIOPort:])
	count := binary.LittleEndian.Uint32(r.b[runIOCount:])
	dataOffset := binary.LittleEndian.Uint64(r.b[runIODataOffset:])
	n := uint64(size) * uint64(count)
	return direction, size, port, r.b[dataOffset : dataOffset+n]
}

// MMIO returns the KVM_EXIT_MMIO payload: the memory-mapped I/O access to
// handle. Data is the fixed 8-byte scratch area KVM_EXIT_MMIO carries
// in-line (unlike KVM_EXIT_IO's separate data region); only its first Len
// bytes are meaningful. For a write, Data holds what the guest wrote; for
// a read, the device must fill Data[:Len] before the next Run call.
func (r *Run) MMIO() (addr uint64, data []byte, isWrite bool) {
	addr = binary.LittleEndian.Uint64(r.b[runMMIOPhysAddr:])
	length := binary.LittleEndian.Uint32(r.b[runMMIOLen:])
	isWrite = r.b[runMMIOIsWrite] != 0
	return addr, r.b[runMMIOData : runMMIOData+uint64(length)], isWrite
}

// FailEntry returns the KVM_EXIT_FAIL_ENTRY payload: the vCPU could not
// even start running (typically wrong/inconsistent sregs — a boot-protocol
// setup mistake). HardwareReason is CPU-vendor-specific and mostly useful
// pasted into a search engine, not decoded further here.
func (r *Run) FailEntry() (hardwareReason uint64, cpu uint32) {
	return binary.LittleEndian.Uint64(r.b[runFailEntryReason:]), binary.LittleEndian.Uint32(r.b[runFailEntryCPU:])
}

// InternalErrorSuberror returns the KVM_EXIT_INTERNAL_ERROR payload's
// suberror code (KVM_INTERNAL_ERROR_*) — a KVM-internal condition, not a
// guest-triggered one.
func (r *Run) InternalErrorSuberror() uint32 {
	return binary.LittleEndian.Uint32(r.b[runInternalSuberror:])
}
