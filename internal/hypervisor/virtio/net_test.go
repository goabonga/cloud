// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package virtio_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/hypervisor/virtio"
)

// fakeTap is an io.ReadWriter standing in for a real tap device's fd:
// Write captures frames sent to it, Read delivers frames pushed through a
// channel (closing it simulates the fd being closed to unblock a pending
// Read, the same way OpenTap's non-blocking/pollable fd does for real).
type fakeTap struct {
	mu      sync.Mutex
	writes  [][]byte
	rx      chan []byte
	readErr error // returned instead of io.EOF once rx is closed, if set
}

func newFakeTap() *fakeTap {
	return &fakeTap{rx: make(chan []byte, 4)}
}

func (t *fakeTap) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.writes = append(t.writes, append([]byte(nil), p...))
	return len(p), nil
}

func (t *fakeTap) Read(p []byte) (int, error) {
	frame, ok := <-t.rx
	if !ok {
		if t.readErr != nil {
			return 0, t.readErr
		}
		return 0, io.EOF
	}
	return copy(p, frame), nil
}

func (t *fakeTap) Writes() [][]byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([][]byte(nil), t.writes...)
}

// A second, independent memory layout (net_test.go's own, distinct from
// queue_test.go's) with separate ring addresses for the rx and tx queues,
// since a Net device uses both at once.
const (
	netRXDesc, netRXAvail, netRXUsed = 0x10000, 0x11000, 0x12000
	netTXDesc, netTXAvail, netTXUsed = 0x13000, 0x14000, 0x15000
	netQueueSize                     = 4
	netDataAddr                      = 0x20000
	netMemSize                       = 0x40000
)

func writeDescAt(mem fakeMemory, descAddr uint64, idx uint16, addr uint64, length uint32, flags, next uint16) {
	b, _ := mem.Slice(descAddr+uint64(idx)*16, 16)
	binary.LittleEndian.PutUint64(b[0:], addr)
	binary.LittleEndian.PutUint32(b[8:], length)
	binary.LittleEndian.PutUint16(b[12:], flags)
	binary.LittleEndian.PutUint16(b[14:], next)
}

func publishAvailAt(mem fakeMemory, availAddr uint64, pos, headDescIdx uint16) {
	entry, _ := mem.Slice(availAddr+4+uint64(pos%netQueueSize)*2, 2)
	binary.LittleEndian.PutUint16(entry, headDescIdx)
	idxBytes, _ := mem.Slice(availAddr+2, 2)
	binary.LittleEndian.PutUint16(idxBytes, pos+1)
}

func readUsedEntryAt(mem fakeMemory, usedAddr uint64, pos uint16) (id, length uint32) {
	b, _ := mem.Slice(usedAddr+4+uint64(pos%netQueueSize)*8, 8)
	return binary.LittleEndian.Uint32(b[0:]), binary.LittleEndian.Uint32(b[4:])
}

// setupNet builds a Net with both queues configured and marked ready
// (QueueSel/Num/Desc/Avail/Used/Ready, the sequence a real driver's
// initialization performs — see Transport's doc comment on the status
// state machine) against a fresh fake tap and memory.
// notified is buffered generously so onInterrupt (called from whichever
// goroutine is servicing a notify or ReadLoop) never blocks on it; tests
// that care about ordering (reading mem after a delivery) receive from it
// instead of polling mem directly from another goroutine — mem itself is
// a plain []byte with no synchronization of its own, so a concurrent
// unsynchronized read from the test goroutine while ReadLoop's goroutine
// writes to it would be a real, race-detector-visible data race, even
// though the equivalent real guest-CPU/host-goroutine relationship isn't
// one Go's race detector can see at all (see queue.go's Push doc comment).
func setupNet(t *testing.T) (n *virtio.Net, mem fakeMemory, tap *fakeTap, interrupts *atomic.Int32, notified chan struct{}) {
	t.Helper()
	mem = newFakeMemory(netMemSize)
	tap = newFakeTap()
	interrupts = new(atomic.Int32)
	notified = make(chan struct{}, 16)
	n = virtio.NewNet([6]byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}, tap, mem, func() {
		interrupts.Add(1)
		notified <- struct{}{}
	})

	configureQueue := func(idx int, desc, avail, used uint64) {
		tr := n.Transport()
		w := func(off uint64, v uint32) {
			b := make([]byte, 4)
			binary.LittleEndian.PutUint32(b, v)
			tr.Write(off, b)
		}
		w(0x030, uint32(idx)) // QueueSel
		w(0x038, netQueueSize)
		w(0x080, uint32(desc))
		w(0x084, 0)
		w(0x090, uint32(avail))
		w(0x094, 0)
		w(0x0a0, uint32(used))
		w(0x0a4, 0)
		w(0x044, 1) // QueueReady
	}
	configureQueue(0, netRXDesc, netRXAvail, netRXUsed)
	configureQueue(1, netTXDesc, netTXAvail, netTXUsed)

	return n, mem, tap, interrupts, notified
}

func TestNetConfigSpace(t *testing.T) {
	mac := [6]byte{0x02, 0x11, 0x22, 0x33, 0x44, 0x55}
	n := virtio.NewNet(mac, newFakeTap(), newFakeMemory(0x1000), nil)

	got := make([]byte, 8)
	n.Transport().Read(virtio.ConfigSpaceOffset, got)
	if !bytes.Equal(got[0:6], mac[:]) {
		t.Errorf("config MAC = % x, want % x", got[0:6], mac)
	}
	if status := binary.LittleEndian.Uint16(got[6:8]); status != 1 {
		t.Errorf("config status = %d, want 1 (link up)", status)
	}
}

func TestNetHandleNotifyTransmitsFrame(t *testing.T) {
	// HandleNotify runs synchronously in this goroutine here (unlike
	// ReadLoop), so there is no cross-goroutine ordering to synchronize
	// via notified — mem is read safely right after the call returns.
	n, mem, tap, interrupts, _ := setupNet(t)

	header := make([]byte, 10) // zeroed virtio_net_hdr
	payload := []byte("hello-from-the-guest")
	copy(mem[netDataAddr:], header)
	copy(mem[netDataAddr+0x100:], payload)
	writeDescAt(mem, netTXDesc, 0, netDataAddr, uint32(len(header)), 1 /* F_NEXT */, 1)
	writeDescAt(mem, netTXDesc, 1, netDataAddr+0x100, uint32(len(payload)), 0, 0)
	publishAvailAt(mem, netTXAvail, 0, 0)

	n.HandleNotify(1) // netQueueTX

	writes := tap.Writes()
	if len(writes) != 1 {
		t.Fatalf("tap received %d writes, want 1", len(writes))
	}
	if string(writes[0]) != string(payload) {
		t.Errorf("transmitted frame = %q, want %q (header stripped)", writes[0], payload)
	}

	id, length := readUsedEntryAt(mem, netTXUsed, 0)
	if id != 0 || length != 0 {
		t.Errorf("tx used entry = {id:%d len:%d}, want {id:0 len:0}", id, length)
	}
	if got := interrupts.Load(); got != 1 {
		t.Errorf("onInterrupt called %d times, want 1", got)
	}
}

func TestNetHandleNotifyIgnoresRXQueue(t *testing.T) {
	n, _, tap, _, _ := setupNet(t)
	n.HandleNotify(0) // netQueueRX: nothing to transmit, must not touch the tap
	if len(tap.Writes()) != 0 {
		t.Error("HandleNotify(netQueueRX) wrote to the tap, want no-op")
	}
}

func TestNetReadLoopDeliversFrame(t *testing.T) {
	n, mem, tap, interrupts, notified := setupNet(t)

	// A single writable rx descriptor, large enough for the header plus
	// a short test frame.
	writeDescAt(mem, netRXDesc, 0, netDataAddr, 256, 2 /* F_WRITE */, 0)
	publishAvailAt(mem, netRXAvail, 0, 0)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- n.ReadLoop(ctx) }()

	frame := []byte("incoming-ethernet-frame")
	tap.rx <- frame

	select {
	case <-notified:
		// onInterrupt happens after Push in deliver, so receiving here
		// establishes a happens-before edge: it's now safe to read mem.
	case <-time.After(2 * time.Second):
		t.Fatal("rx delivery did not complete within the deadline")
	}

	id, length := readUsedEntryAt(mem, netRXUsed, 0)
	if id != 0 {
		t.Errorf("rx used entry id = %d, want 0", id)
	}
	wantLen := uint32(10 + len(frame)) // virtio_net_hdr + frame
	if length != wantLen {
		t.Errorf("rx used entry len = %d, want %d", length, wantLen)
	}
	got, _ := mem.Slice(netDataAddr+10, len(frame))
	if string(got) != string(frame) {
		t.Errorf("delivered frame = %q, want %q", got, frame)
	}
	if got := interrupts.Load(); got != 1 {
		t.Errorf("onInterrupt called %d times, want 1", got)
	}

	close(tap.rx)
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("ReadLoop returned %v after the tap closed, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadLoop did not return after the tap closed")
	}
}

func TestNetReadLoopDropsWithoutRXBuffer(t *testing.T) {
	// No rx descriptor published at all — deliver must not panic, and
	// ReadLoop must keep running (the frame is simply dropped).
	n, _, tap, _, _ := setupNet(t)
	// Undo the rx queue's readiness from setupNet to exercise the
	// "driver hasn't supplied buffers" path specifically, not just an
	// empty avail ring.
	tr := n.Transport()
	w := func(off uint64, v uint32) {
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, v)
		tr.Write(off, b)
	}
	w(0x030, 0) // QueueSel = rx
	w(0x044, 0) // QueueReady = false

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- n.ReadLoop(ctx) }()

	tap.rx <- []byte("dropped")
	time.Sleep(50 * time.Millisecond) // let ReadLoop process it (or not panic trying)
	cancel()
	close(tap.rx)

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("ReadLoop returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadLoop did not return after cancellation")
	}
}

// TestNetReadLoopStopsCleanlyOnClosedFd exercises Close() being called
// (closing the tap fd) without ctx being cancelled first — the case a
// caller using Machine.Close directly, rather than cmd/hypervisor's
// handleShutdown (which always cancels ctx first), hits. A real *os.File
// read after Close returns an fs.ErrClosed-wrapping error, not io.EOF.
func TestNetReadLoopStopsCleanlyOnClosedFd(t *testing.T) {
	n, _, tap, _, _ := setupNet(t)
	tap.readErr = fmt.Errorf("read tap0: %w", fs.ErrClosed)

	ctx := context.Background() // deliberately never cancelled
	done := make(chan error, 1)
	go func() { done <- n.ReadLoop(ctx) }()

	close(tap.rx)

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("ReadLoop returned %v after a closed-fd read error, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadLoop did not return after the tap's read failed with fs.ErrClosed")
	}
}
