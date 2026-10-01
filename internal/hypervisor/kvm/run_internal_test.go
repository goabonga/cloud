// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package kvm

import (
	"encoding/binary"
	"testing"
)

func TestRunExitReason(t *testing.T) {
	r := &Run{b: make([]byte, 0x40)}
	binary.LittleEndian.PutUint32(r.b[runExitReason:], ExitHLT)
	if got := r.ExitReason(); got != ExitHLT {
		t.Errorf("ExitReason() = %d, want %d", got, ExitHLT)
	}
}

func TestRunIO(t *testing.T) {
	r := &Run{b: make([]byte, 0x500)}
	r.b[runIODirection] = IODirOut
	r.b[runIOSize] = 1
	binary.LittleEndian.PutUint16(r.b[runIOPort:], 0x3f8)
	binary.LittleEndian.PutUint32(r.b[runIOCount:], 3)
	binary.LittleEndian.PutUint64(r.b[runIODataOffset:], 0x400)
	copy(r.b[0x400:], []byte{'h', 'i', '!'})

	dir, size, port, data := r.IO()
	if dir != IODirOut {
		t.Errorf("direction = %d, want IODirOut", dir)
	}
	if size != 1 {
		t.Errorf("size = %d, want 1", size)
	}
	if port != 0x3f8 {
		t.Errorf("port = 0x%x, want 0x3f8", port)
	}
	if string(data) != "hi!" {
		t.Errorf("data = %q, want %q", data, "hi!")
	}
}

func TestRunMMIO(t *testing.T) {
	r := &Run{b: make([]byte, 0x40)}
	binary.LittleEndian.PutUint64(r.b[runMMIOPhysAddr:], 0xd0000000)
	binary.LittleEndian.PutUint32(r.b[runMMIOLen:], 4)
	r.b[runMMIOIsWrite] = 1
	binary.LittleEndian.PutUint32(r.b[runMMIOData:], 0xdeadbeef)

	addr, data, isWrite := r.MMIO()
	if addr != 0xd0000000 {
		t.Errorf("addr = 0x%x, want 0xd0000000", addr)
	}
	if !isWrite {
		t.Error("isWrite = false, want true")
	}
	if len(data) != 4 {
		t.Fatalf("len(data) = %d, want 4", len(data))
	}
	if got := binary.LittleEndian.Uint32(data); got != 0xdeadbeef {
		t.Errorf("data = 0x%x, want 0xdeadbeef", got)
	}
}

func TestRunFailEntry(t *testing.T) {
	r := &Run{b: make([]byte, 0x40)}
	binary.LittleEndian.PutUint64(r.b[runFailEntryReason:], 0x80000021)
	binary.LittleEndian.PutUint32(r.b[runFailEntryCPU:], 1)

	reason, cpu := r.FailEntry()
	if reason != 0x80000021 {
		t.Errorf("reason = 0x%x, want 0x80000021", reason)
	}
	if cpu != 1 {
		t.Errorf("cpu = %d, want 1", cpu)
	}
}

func TestRunInternalErrorSuberror(t *testing.T) {
	r := &Run{b: make([]byte, 0x40)}
	binary.LittleEndian.PutUint32(r.b[runInternalSuberror:], 1)
	if got := r.InternalErrorSuberror(); got != 1 {
		t.Errorf("InternalErrorSuberror() = %d, want 1", got)
	}
}
