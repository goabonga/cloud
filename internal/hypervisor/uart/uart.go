// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package uart emulates a minimal 16550A-compatible serial port: enough
// register state for a Linux guest's 8250 driver (and its earlycon, which
// polls rather than waiting for interrupts) to treat it as a working
// console. It is pure register/byte-stream logic with no knowledge of
// ports, IRQs or KVM — internal/hypervisor maps it onto COM1's I/O range
// and asserts its interrupt line.
package uart

import (
	"io"
	"sync"
)

// Register offsets from the UART's base I/O port (COM1 is 0x3f8), per the
// 16550 programming model. Offsets 0 and 1 are overloaded with the baud
// rate divisor latch when LCR's DLAB bit is set.
const (
	RegData    = 0 // THR (write) / RBR (read); DLL when DLAB=1
	RegIER     = 1 // interrupt enable; DLM when DLAB=1
	RegIIRFCR  = 2 // IIR (read) / FCR (write)
	RegLCR     = 3 // line control; bit 7 is DLAB
	RegMCR     = 4 // modem control
	RegLSR     = 5 // line status
	RegMSR     = 6 // modem status
	RegScratch = 7
)

const (
	lcrDLAB = 1 << 7

	ierTHREInterrupt = 1 << 1 // "transmitter holding register empty" interrupt enable

	lsrDataReady     = 1 << 0
	lsrTHREmpty      = 1 << 5 // ready to accept another byte to transmit
	lsrTransmitEmpty = 1 << 6 // transmitter fully idle

	iirNoInterruptPending = 1 << 0
)

// UART is one emulated 16550A. Every transmitted byte is written to Out
// immediately — there is no FIFO delay to model since this is a
// paravirtual device with no real transmission latency to hide. Safe for
// concurrent use: with more than one vCPU, any of them may be the one
// whose guest code happens to touch the console at a given moment.
type UART struct {
	out io.Writer

	mu                     sync.Mutex
	ier, lcr, mcr, scratch byte
	divisorLatch           uint16
}

// New returns a UART that writes transmitted bytes to out. A nil out
// discards them (io.Discard), which is a valid, if silent, console.
func New(out io.Writer) *UART {
	if out == nil {
		out = io.Discard
	}
	return &UART{out: out}
}

// Write handles a guest write to register offset off (0-7, as decoded from
// the I/O port by the caller). It returns whether the device now wants its
// interrupt line asserted — true only for a data-register write with the
// THRE interrupt enabled, since this device is always immediately ready
// for the next byte.
func (u *UART) Write(off uint8, val byte) (wantInterrupt bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	switch off {
	case RegData:
		if u.lcr&lcrDLAB != 0 {
			u.divisorLatch = u.divisorLatch&0xff00 | uint16(val)
			return false
		}
		_, _ = u.out.Write([]byte{val}) // best-effort: a full console pipe is not this device's problem
		return u.ier&ierTHREInterrupt != 0
	case RegIER:
		if u.lcr&lcrDLAB != 0 {
			u.divisorLatch = u.divisorLatch&0x00ff | uint16(val)<<8
			return false
		}
		u.ier = val & 0x0f // only the low 4 bits are defined
	case RegIIRFCR:
		// FCR (FIFO control): no FIFO is modeled, so every mode it
		// could select behaves identically; nothing to store.
	case RegLCR:
		u.lcr = val
	case RegMCR:
		u.mcr = val
	case RegLSR, RegMSR:
		// Read-only on real hardware; silently ignore a write.
	case RegScratch:
		u.scratch = val
	}
	return false
}

// Read handles a guest read from register offset off. There is no input
// path yet (nothing feeds the guest keyboard/stdin), so RBR always reads
// as "no data" and LSR always reports the transmitter ready — enough for
// an output-only console.
func (u *UART) Read(off uint8) byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	switch off {
	case RegData:
		if u.lcr&lcrDLAB != 0 {
			return byte(u.divisorLatch)
		}
		return 0 // RBR: no received data
	case RegIER:
		if u.lcr&lcrDLAB != 0 {
			return byte(u.divisorLatch >> 8)
		}
		return u.ier
	case RegIIRFCR:
		return iirNoInterruptPending
	case RegLCR:
		return u.lcr
	case RegMCR:
		return u.mcr
	case RegLSR:
		return lsrTHREmpty | lsrTransmitEmpty // never lsrDataReady: no input path
	case RegMSR:
		return 0
	case RegScratch:
		return u.scratch
	}
	return 0xff // real hardware would also return bus-float garbage for an invalid offset
}
