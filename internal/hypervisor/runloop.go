// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package hypervisor

import (
	"context"
	"fmt"
	"runtime"

	"github.com/goabonga/infrastructure/internal/hypervisor/kvm"
)

// ExitEvent describes one vCPU exit Run reported to its onExit callback.
// Which fields are meaningful depends on Reason (a kvm.Exit* constant):
// IODirection/IOPort/IOData for kvm.ExitIO, MMIOAddr/MMIOData/MMIOWrite
// for kvm.ExitMMIO; the rest are zero.
type ExitEvent struct {
	Reason uint32

	IODirection uint8
	IOPort      uint16
	IOSize      uint8
	IOData      []byte

	MMIOAddr  uint64
	MMIOData  []byte
	MMIOWrite bool
}

// Run drives vCPU 0's run loop until ctx is cancelled or a fatal exit
// occurs (kvm.ExitShutdown, kvm.ExitFailEntry or kvm.ExitInternalError —
// each means the vCPU cannot usefully continue, typically a boot-protocol
// setup mistake rather than anything a guest triggered), returning nil in
// the former case and a descriptive error in the latter.
//
// kvm.ExitHLT is treated as idle and resumed without surfacing an event.
// Every other exit reason (today, only kvm.ExitIO and kvm.ExitMMIO) is
// reported via onExit — which may be nil — and then resumed without this
// package acting on it: there is no device model yet (added in later
// milestones), so an unhandled port or MMIO read gets whatever was already
// in its (zeroed) data buffer and a write is silently dropped. onExit may
// mutate IOData/MMIOData in place to answer a read before Run resumes.
//
// Run must be called from a goroutine the caller does not otherwise use
// for KVM ioctls: it locks the calling goroutine to its OS thread for as
// long as it runs (ioctl(KVM_RUN) and friends are only valid issued from
// the same thread a vCPU was created from, and Go's scheduler is
// otherwise free to migrate a goroutine between syscalls).
func (m *Machine) Run(ctx context.Context, onExit func(*ExitEvent)) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	v := m.vcpus[0]
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := v.kv.Run(); err != nil {
			return fmt.Errorf("hypervisor: %w", err)
		}

		switch reason := v.run.ExitReason(); reason {
		case kvm.ExitHLT:
			// Idle; KVM_RUN resumes on the next real event (an
			// interrupt, since IF is off at boot, would normally be
			// the only way out — acceptable to keep looping here since
			// the in-kernel irqchip delivers those without our
			// involvement).

		case kvm.ExitIO:
			if onExit != nil {
				dir, size, port, data := v.run.IO()
				onExit(&ExitEvent{Reason: reason, IODirection: dir, IOSize: size, IOPort: port, IOData: data})
			}

		case kvm.ExitMMIO:
			if onExit != nil {
				addr, data, isWrite := v.run.MMIO()
				onExit(&ExitEvent{Reason: reason, MMIOAddr: addr, MMIOData: data, MMIOWrite: isWrite})
			}

		case kvm.ExitShutdown:
			return fmt.Errorf("hypervisor: vcpu shutdown (likely a triple fault — check the boot-protocol register setup)")

		case kvm.ExitFailEntry:
			hwReason, cpu := v.run.FailEntry()
			return fmt.Errorf("hypervisor: vcpu %d failed to enter: hardware reason 0x%x (likely invalid sregs)", cpu, hwReason)

		case kvm.ExitInternalError:
			return fmt.Errorf("hypervisor: internal KVM error, suberror %d", v.run.InternalErrorSuberror())

		default:
			return fmt.Errorf("hypervisor: unexpected vcpu exit reason %d", reason)
		}
	}
}
