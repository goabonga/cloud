// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package uart_test

import (
	"bytes"
	"testing"

	"github.com/goabonga/infrastructure/internal/hypervisor/uart"
)

func TestWriteDataTransmitsByte(t *testing.T) {
	var out bytes.Buffer
	u := uart.New(&out)

	u.Write(uart.RegData, 'h')
	u.Write(uart.RegData, 'i')

	if got := out.String(); got != "hi" {
		t.Errorf("transmitted bytes = %q, want %q", got, "hi")
	}
}

func TestWriteDataInterruptGatedByIER(t *testing.T) {
	u := uart.New(nil)

	if irq := u.Write(uart.RegData, 'x'); irq {
		t.Error("Write(RegData) wanted interrupt with THRE interrupt disabled (default IER=0)")
	}

	u.Write(uart.RegIER, 0x02) // enable THRE interrupt
	if irq := u.Write(uart.RegData, 'x'); !irq {
		t.Error("Write(RegData) did not want interrupt with THRE interrupt enabled")
	}
}

func TestLSRAlwaysReadyToTransmit(t *testing.T) {
	u := uart.New(nil)
	const wantLSR = 0x20 | 0x40 // THRE | TEMT
	if got := u.Read(uart.RegLSR); got != wantLSR {
		t.Errorf("LSR = 0x%02x, want 0x%02x", got, wantLSR)
	}
}

func TestRBRHasNoData(t *testing.T) {
	u := uart.New(nil)
	if got := u.Read(uart.RegData); got != 0 {
		t.Errorf("RBR = 0x%02x, want 0 (no input path)", got)
	}
}

func TestDivisorLatchRoundTrip(t *testing.T) {
	u := uart.New(nil)
	u.Write(uart.RegLCR, 0x80) // set DLAB
	u.Write(uart.RegData, 0x01)
	u.Write(uart.RegIER, 0x00)
	u.Write(uart.RegLCR, 0x00) // clear DLAB

	u.Write(uart.RegLCR, 0x80)
	lo := u.Read(uart.RegData)
	hi := u.Read(uart.RegIER)
	if lo != 0x01 || hi != 0x00 {
		t.Errorf("divisor latch round-trip = 0x%02x%02x, want 0x0001", hi, lo)
	}
}

func TestScratchRegisterRoundTrip(t *testing.T) {
	u := uart.New(nil)
	u.Write(uart.RegScratch, 0x42)
	if got := u.Read(uart.RegScratch); got != 0x42 {
		t.Errorf("scratch register = 0x%02x, want 0x42", got)
	}
}

func TestIERMasksUndefinedBits(t *testing.T) {
	u := uart.New(nil)
	u.Write(uart.RegIER, 0xff)
	if got := u.Read(uart.RegIER); got != 0x0f {
		t.Errorf("IER = 0x%02x, want 0x0f (only the low 4 bits are defined)", got)
	}
}
