// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package hypervisor

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	"github.com/goabonga/infrastructure/internal/hypervisor/kvm"
)

// ExitEvent describes one vCPU exit Run reported to its onExit callback.
// Which fields are meaningful depends on Reason (a kvm.Exit* constant):
// IODirection/IOPort/IOData for kvm.ExitIO, MMIOAddr/MMIOData/MMIOWrite
// for kvm.ExitMMIO; the rest are zero. onExit may be called concurrently
// from different vCPUs — see Run.
type ExitEvent struct {
	VCPU   int
	Reason uint32

	IODirection uint8
	IOPort      uint16
	IOSize      uint8
	IOData      []byte

	MMIOAddr  uint64
	MMIOData  []byte
	MMIOWrite bool
}

// Run drives every vCPU's run loop, each in its own goroutine, until ctx is
// cancelled or any one of them hits a fatal exit (kvm.ExitShutdown,
// kvm.ExitFailEntry or kvm.ExitInternalError — each means that vCPU cannot
// usefully continue, typically a boot-protocol setup mistake rather than
// anything a guest triggered): Run cancels every other vCPU's loop too in
// that case and returns the first such error. A clean stop (ctx cancelled
// before any fatal exit) returns nil.
//
// Every vCPU is configured identically at creation (see bootVCPU) and
// started the same way, including vCPUs beyond 0: with the in-kernel
// irqchip this package creates, KVM itself keeps every non-boot vCPU in
// KVM_MP_STATE_UNINITIALIZED until it receives a real INIT-SIPI-SIPI from
// the guest's own SMP bring-up code (sent once the booted kernel reaches
// its own secondary-CPU startup, over the in-kernel LAPIC, once the guest
// decides to) — so calling KVM_RUN on them before that is inert, not a
// race against vCPU 0 executing the same entry point twice. This package
// does not implement any part of that sequence itself; it is entirely
// KVM's and the guest kernel's doing (see
// docs/architecture/go-hypervisor.md for the fuller account and the
// sources that confirm it, since the KVM API documentation does not spell
// this interaction out explicitly).
//
// kvm.ExitHLT is treated as idle and resumed without surfacing an event.
// A kvm.ExitIO on COM1's port range is answered by the emulated UART
// (console.go), which is safe for concurrent use from multiple vCPUs, and
// never reaches onExit. Every other exit reason (today, any other
// kvm.ExitIO port, and all of kvm.ExitMMIO — no MMIO device exists yet,
// added in a later milestone) is reported via onExit — which may be nil —
// and then resumed without this package acting on it: an unhandled port or
// MMIO read gets whatever was already in its (zeroed) data buffer and a
// write is silently dropped. onExit may mutate IOData/MMIOData in place to
// answer a read before Run resumes, but must be safe to call from more
// than one goroutine at once if there is more than one vCPU.
func (m *Machine) Run(ctx context.Context, onExit func(*ExitEvent)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error, len(m.vcpus))
	var wg sync.WaitGroup
	for _, v := range m.vcpus {
		wg.Add(1)
		go func(v *vcpu) {
			defer wg.Done()
			errs <- m.runVCPU(ctx, v, onExit)
		}(v)
	}
	go func() {
		wg.Wait()
		close(errs)
	}()

	var firstErr error
	for err := range errs {
		if err != nil && firstErr == nil {
			firstErr = err
			cancel() // stop every other vcpu promptly rather than waiting out its own idle loop
		}
	}
	return firstErr
}

// runVCPU is one vCPU's run loop; see Run's doc comment for the exit-reason
// handling and the AP-vCPU reasoning this relies on.
//
// It must be called from a goroutine not otherwise used for KVM ioctls: it
// locks the calling goroutine to its OS thread for as long as it runs
// (ioctl(KVM_RUN) and friends are only valid issued from the same thread a
// vCPU was created from, and Go's scheduler is otherwise free to migrate a
// goroutine between syscalls).
func (m *Machine) runVCPU(ctx context.Context, v *vcpu, onExit func(*ExitEvent)) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := v.kv.Run(); err != nil {
			return fmt.Errorf("hypervisor: vcpu %d: %w", v.kv.ID(), err)
		}

		switch reason := v.run.ExitReason(); reason {
		case kvm.ExitHLT:
			// Idle; KVM_RUN resumes on the next real event (an
			// interrupt, since IF is off at boot, would normally be
			// the only way out — acceptable to keep looping here since
			// the in-kernel irqchip delivers those without our
			// involvement).

		case kvm.ExitIO:
			dir, size, port, data := v.run.IO()
			if !m.serveIO(port, dir, data) && onExit != nil {
				onExit(&ExitEvent{VCPU: v.kv.ID(), Reason: reason, IODirection: dir, IOSize: size, IOPort: port, IOData: data})
			}

		case kvm.ExitMMIO:
			if onExit != nil {
				addr, data, isWrite := v.run.MMIO()
				onExit(&ExitEvent{VCPU: v.kv.ID(), Reason: reason, MMIOAddr: addr, MMIOData: data, MMIOWrite: isWrite})
			}

		case kvm.ExitShutdown:
			return fmt.Errorf("hypervisor: vcpu %d shutdown (likely a triple fault — check the boot-protocol register setup)", v.kv.ID())

		case kvm.ExitFailEntry:
			hwReason, cpu := v.run.FailEntry()
			return fmt.Errorf("hypervisor: vcpu %d failed to enter: hardware reason 0x%x (likely invalid sregs)", cpu, hwReason)

		case kvm.ExitInternalError:
			return fmt.Errorf("hypervisor: vcpu %d: internal KVM error, suberror %d", v.kv.ID(), v.run.InternalErrorSuberror())

		default:
			return fmt.Errorf("hypervisor: vcpu %d: unexpected exit reason %d", v.kv.ID(), reason)
		}
	}
}
