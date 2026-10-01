// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package hypervisor

import "testing"

func TestMachineSlice(t *testing.T) {
	m := &Machine{mem: make([]byte, 0x1000)}
	copy(m.mem[0x10:], []byte("hello"))

	got, err := m.Slice(0x10, 5)
	if err != nil {
		t.Fatalf("Slice: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("Slice(0x10, 5) = %q, want \"hello\"", got)
	}

	// The returned slice aliases m.mem: writing through it must be
	// visible there too (what makes it usable as device-writable memory).
	got[0] = 'H'
	if m.mem[0x10] != 'H' {
		t.Error("writing through the returned slice did not affect m.mem")
	}
}

func TestMachineSliceOutOfBounds(t *testing.T) {
	m := &Machine{mem: make([]byte, 0x1000)}
	if _, err := m.Slice(0x0ffc, 8); err == nil {
		t.Fatal("Slice succeeded reading past the end of memory, want error")
	}
	if _, err := m.Slice(0x2000, 1); err == nil {
		t.Fatal("Slice succeeded with an address past the end of memory, want error")
	}
}
