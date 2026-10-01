// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package virtio_test

import (
	"encoding/binary"
	"testing"

	"github.com/goabonga/infrastructure/internal/hypervisor/virtio"
)

// A small, fixed memory layout every test in this file shares: a 4-entry
// queue with its descriptor table, avail ring and used ring at distinct,
// generously spaced addresses, and a data area past all of them for
// descriptor payloads.
const (
	testDescAddr  = 0x1000
	testAvailAddr = 0x2000
	testUsedAddr  = 0x3000
	testDataAddr  = 0x4000
	testQueueSize = 4
	testMemSize   = 0x8000
)

func newTestQueue(t *testing.T) (fakeMemory, *virtio.VirtQueue) {
	t.Helper()
	mem := newFakeMemory(testMemSize)
	vq := virtio.NewVirtQueue(mem, virtio.Queue{
		Size: testQueueSize, Ready: true,
		Desc: testDescAddr, Avail: testAvailAddr, Used: testUsedAddr,
	})
	return mem, vq
}

func writeDesc(mem fakeMemory, idx uint16, addr uint64, length uint32, flags, next uint16) {
	b, _ := mem.Slice(testDescAddr+uint64(idx)*16, 16)
	binary.LittleEndian.PutUint64(b[0:], addr)
	binary.LittleEndian.PutUint32(b[8:], length)
	binary.LittleEndian.PutUint16(b[12:], flags)
	binary.LittleEndian.PutUint16(b[14:], next)
}

// publishAvail appends headDescIdx to the avail ring and bumps avail.idx —
// the driver side of "make this request available to the device".
func publishAvail(mem fakeMemory, pos uint16, headDescIdx uint16) {
	entry, _ := mem.Slice(testAvailAddr+4+uint64(pos%testQueueSize)*2, 2)
	binary.LittleEndian.PutUint16(entry, headDescIdx)
	idxBytes, _ := mem.Slice(testAvailAddr+2, 2)
	binary.LittleEndian.PutUint16(idxBytes, pos+1)
}

func readUsedEntry(mem fakeMemory, pos uint16) (id, length uint32) {
	b, _ := mem.Slice(testUsedAddr+4+uint64(pos%testQueueSize)*8, 8)
	return binary.LittleEndian.Uint32(b[0:]), binary.LittleEndian.Uint32(b[4:])
}

func readUsedIdx(mem fakeMemory) uint16 {
	b, _ := mem.Slice(testUsedAddr+2, 2)
	return binary.LittleEndian.Uint16(b)
}

func TestVirtQueuePopNothingAvailable(t *testing.T) {
	_, vq := newTestQueue(t)
	_, ok, err := vq.Pop()
	if err != nil {
		t.Fatalf("Pop: %v", err)
	}
	if ok {
		t.Fatal("Pop returned ok=true with nothing published")
	}
}

func TestVirtQueuePopSingleDescriptor(t *testing.T) {
	mem, vq := newTestQueue(t)
	copy(mem[testDataAddr:], "hello")
	writeDesc(mem, 0, testDataAddr, 5, 0, 0) // no NEXT, not writable
	publishAvail(mem, 0, 0)

	chain, ok, err := vq.Pop()
	if err != nil {
		t.Fatalf("Pop: %v", err)
	}
	if !ok {
		t.Fatal("Pop returned ok=false with a request published")
	}
	if chain.HeadIndex != 0 {
		t.Errorf("HeadIndex = %d, want 0", chain.HeadIndex)
	}
	if len(chain.Segments) != 1 {
		t.Fatalf("len(Segments) = %d, want 1", len(chain.Segments))
	}
	if string(chain.Segments[0].Data) != "hello" {
		t.Errorf("Segments[0].Data = %q, want \"hello\"", chain.Segments[0].Data)
	}
	if chain.Segments[0].Writable {
		t.Error("Segments[0].Writable = true, want false (no F_WRITE flag)")
	}

	if _, ok, err := vq.Pop(); err != nil || ok {
		t.Fatalf("second Pop: ok=%v err=%v, want ok=false err=nil (nothing new)", ok, err)
	}
}

func TestVirtQueuePopChainedDescriptors(t *testing.T) {
	// Mimics a virtio-blk-style request: a read-only header descriptor
	// chained to a writable data descriptor.
	mem, vq := newTestQueue(t)
	copy(mem[testDataAddr:], "request-header")
	writeDesc(mem, 0, testDataAddr, 14, 1 /* F_NEXT */, 1)
	writeDesc(mem, 1, testDataAddr+0x100, 32, 2 /* F_WRITE */, 0)
	publishAvail(mem, 0, 0)

	chain, ok, err := vq.Pop()
	if err != nil || !ok {
		t.Fatalf("Pop: ok=%v err=%v", ok, err)
	}
	if len(chain.Segments) != 2 {
		t.Fatalf("len(Segments) = %d, want 2", len(chain.Segments))
	}
	if chain.Segments[0].Writable {
		t.Error("Segments[0] (header) is writable, want read-only")
	}
	if !chain.Segments[1].Writable {
		t.Error("Segments[1] (data) is read-only, want writable")
	}
	if len(chain.Segments[1].Data) != 32 {
		t.Errorf("len(Segments[1].Data) = %d, want 32", len(chain.Segments[1].Data))
	}
	if got := chain.TotalWritableLen(); got != 32 {
		t.Errorf("TotalWritableLen() = %d, want 32", got)
	}
}

func TestVirtQueuePopMultipleRequests(t *testing.T) {
	mem, vq := newTestQueue(t)
	writeDesc(mem, 0, testDataAddr, 1, 0, 0)
	writeDesc(mem, 1, testDataAddr+1, 1, 0, 0)
	publishAvail(mem, 0, 0)
	publishAvail(mem, 1, 1)

	first, ok, err := vq.Pop()
	if err != nil || !ok || first.HeadIndex != 0 {
		t.Fatalf("first Pop: HeadIndex=%d ok=%v err=%v, want 0/true/nil", first.HeadIndex, ok, err)
	}
	second, ok, err := vq.Pop()
	if err != nil || !ok || second.HeadIndex != 1 {
		t.Fatalf("second Pop: HeadIndex=%d ok=%v err=%v, want 1/true/nil", second.HeadIndex, ok, err)
	}
	if _, ok, _ := vq.Pop(); ok {
		t.Fatal("third Pop returned ok=true, want false (only 2 requests were published)")
	}
}

func TestVirtQueueDetectsCyclicChain(t *testing.T) {
	mem, vq := newTestQueue(t)
	writeDesc(mem, 0, testDataAddr, 1, 1 /* F_NEXT */, 0) // points to itself
	publishAvail(mem, 0, 0)

	_, _, err := vq.Pop()
	if err == nil {
		t.Fatal("Pop succeeded on a self-referential descriptor chain, want an error")
	}
}

func TestVirtQueuePush(t *testing.T) {
	mem, vq := newTestQueue(t)
	if err := vq.Push(3, 42); err != nil {
		t.Fatalf("Push: %v", err)
	}
	id, length := readUsedEntry(mem, 0)
	if id != 3 || length != 42 {
		t.Errorf("used entry = {id:%d len:%d}, want {id:3 len:42}", id, length)
	}
	if got := readUsedIdx(mem); got != 1 {
		t.Errorf("used.idx = %d, want 1", got)
	}

	if err := vq.Push(1, 10); err != nil {
		t.Fatalf("second Push: %v", err)
	}
	if got := readUsedIdx(mem); got != 2 {
		t.Errorf("used.idx after second Push = %d, want 2", got)
	}
	id, length = readUsedEntry(mem, 1)
	if id != 1 || length != 10 {
		t.Errorf("second used entry = {id:%d len:%d}, want {id:1 len:10}", id, length)
	}
}
