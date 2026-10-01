// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package hypervisor

import "github.com/goabonga/infrastructure/internal/hypervisor/kvm"

// COM1's legacy ISA I/O range and interrupt line (GSI 4 — pre-routed to
// this legacy value by the default IOAPIC configuration KVM_CREATE_IRQCHIP
// sets up, same as real hardware).
const (
	com1Base = 0x3f8
	com1End  = 0x3ff
	com1GSI  = 4
)

// serveIO answers a port I/O access if port falls in COM1's range,
// reporting whether it did (an unhandled port is still relayed to Run's
// onExit callback). data is Run.IO's data slice: for IODirOut it holds
// what the guest wrote, for IODirIn this method fills it with what the
// guest should read.
func (m *Machine) serveIO(port uint16, dir uint8, data []byte) bool {
	if port < com1Base || port > com1End {
		return false
	}
	off := uint8(port - com1Base)

	if dir == kvm.IODirIn {
		for i := range data {
			data[i] = m.console.Read(off)
		}
		return true
	}

	wantInterrupt := false
	for _, b := range data {
		if m.console.Write(off, b) {
			wantInterrupt = true
		}
	}
	if wantInterrupt {
		m.pulseIRQ(com1GSI)
	}
	return true
}

// pulseIRQ asserts then immediately deasserts gsi, the edge-triggered
// convention legacy ISA lines (routed through the default IOAPIC
// configuration) use: a single transition is one interrupt, with no level
// to hold. Errors are logged nowhere and ignored — a failed interrupt
// injection on an emulated 16550 is not worth tearing the VM down for, and
// the guest's own earlycon polls LSR rather than depending on it.
func (m *Machine) pulseIRQ(gsi uint32) {
	_ = m.vm.IRQLine(gsi, 1)
	_ = m.vm.IRQLine(gsi, 0)
}
