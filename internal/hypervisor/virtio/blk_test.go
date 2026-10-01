// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package virtio_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"sync"
	"testing"

	"github.com/goabonga/infrastructure/internal/hypervisor/virtio"
)

// fakeBlockBackend is an in-memory BlockBackend, growing on WriteAt past
// its current end the way a real sparse file would.
type fakeBlockBackend struct {
	mu        sync.Mutex
	data      []byte
	syncCalls int
}

func newFakeBlockBackend(initial []byte) *fakeBlockBackend {
	return &fakeBlockBackend{data: append([]byte(nil), initial...)}
}

func (f *fakeBlockBackend) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	return copy(p, f.data[off:]), nil
}

func (f *fakeBlockBackend) WriteAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if end := off + int64(len(p)); end > int64(len(f.data)) {
		grown := make([]byte, end)
		copy(grown, f.data)
		f.data = grown
	}
	return copy(f.data[off:], p), nil
}

func (f *fakeBlockBackend) Sync() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncCalls++
	return nil
}

const (
	blkDesc, blkAvail, blkUsed = 0x30000, 0x31000, 0x32000
	blkQueueSize               = 4
	blkDataAddr                = 0x40000
)

// writeBlkRequest builds a 3-descriptor chain at index 0 (header,
// read-only) -> index 1 (data) -> index 2 (status, writable), publishes
// it on the queue, and returns the data descriptor's address for the
// caller to pre-populate (OUT) or later read back (IN). dataWritable
// should be true for VIRTIO_BLK_T_IN (the device fills it) and false for
// VIRTIO_BLK_T_OUT (the driver already did).
func writeBlkRequest(mem fakeMemory, reqType uint32, sector uint64, dataLen uint32, dataWritable bool) (dataAddr, statusAddr uint64) {
	header := make([]byte, 16)
	binary.LittleEndian.PutUint32(header[0:], reqType)
	binary.LittleEndian.PutUint64(header[8:], sector)
	copy(mem[blkDataAddr:], header)

	dataAddr = blkDataAddr + 0x100
	statusAddr = blkDataAddr + 0x200

	writeDescAt(mem, blkDesc, 0, blkDataAddr, 16, 1 /* F_NEXT */, 1)
	dataFlags := uint16(1) // F_NEXT
	if dataWritable {
		dataFlags |= 2 // F_WRITE
	}
	writeDescAt(mem, blkDesc, 1, dataAddr, dataLen, dataFlags, 2)
	writeDescAt(mem, blkDesc, 2, statusAddr, 1, 2 /* F_WRITE */, 0)
	publishAvailAt(mem, blkAvail, 0, 0)
	return dataAddr, statusAddr
}

func newTestBlk(t *testing.T, backend *fakeBlockBackend, capacity int64, readonly bool) (*virtio.Blk, fakeMemory) {
	t.Helper()
	mem := newFakeMemory(0x80000)
	b := virtio.NewBlk(backend, capacity, readonly, mem, nil)

	tr := b.Transport()
	w := func(off uint64, v uint32) {
		data := make([]byte, 4)
		binary.LittleEndian.PutUint32(data, v)
		tr.Write(off, data)
	}
	w(0x030, 0) // QueueSel
	w(0x038, blkQueueSize)
	w(0x080, uint32(blkDesc))
	w(0x084, 0)
	w(0x090, uint32(blkAvail))
	w(0x094, 0)
	w(0x0a0, uint32(blkUsed))
	w(0x0a4, 0)
	w(0x044, 1) // QueueReady

	return b, mem
}

func TestBlkConfigSpace(t *testing.T) {
	const sectorSize = 512
	b, _ := newTestBlk(t, newFakeBlockBackend(nil), 10*sectorSize, false)
	got := make([]byte, 8)
	b.Transport().Read(virtio.ConfigSpaceOffset, got)
	if cap := binary.LittleEndian.Uint64(got); cap != 10 {
		t.Errorf("config capacity = %d sectors, want 10", cap)
	}
}

func TestBlkRead(t *testing.T) {
	backend := newFakeBlockBackend(bytes.Repeat([]byte{0xAB}, 4096))
	copy(backend.data[1024:], []byte("sector-two-data!"))
	b, mem := newTestBlk(t, backend, 4096, false)

	dataAddr, statusAddr := writeBlkRequest(mem, 0 /* VIRTIO_BLK_T_IN */, 2, 16, true)

	b.HandleNotify(0)

	got, _ := mem.Slice(dataAddr, 16)
	if string(got) != "sector-two-data!" {
		t.Errorf("read data = %q, want %q", got, "sector-two-data!")
	}
	status, _ := mem.Slice(statusAddr, 1)
	if status[0] != 0 {
		t.Errorf("status = %d, want 0 (OK)", status[0])
	}
	id, length := readUsedEntryAt(mem, blkUsed, 0)
	if id != 0 || length != 17 { // 16 data bytes + 1 status byte
		t.Errorf("used entry = {id:%d len:%d}, want {id:0 len:17}", id, length)
	}
}

func TestBlkWrite(t *testing.T) {
	backend := newFakeBlockBackend(make([]byte, 4096))
	b, mem := newTestBlk(t, backend, 4096, false)

	payload := []byte("written-by-the-guest")
	dataAddr, statusAddr := writeBlkRequest(mem, 1 /* VIRTIO_BLK_T_OUT */, 3, uint32(len(payload)), false)
	copy(mem[dataAddr:], payload)

	b.HandleNotify(0)

	const wantOffset = 3 * 512
	if got := backend.data[wantOffset : wantOffset+len(payload)]; string(got) != string(payload) {
		t.Errorf("backend data at offset %d = %q, want %q", wantOffset, got, payload)
	}
	status, _ := mem.Slice(statusAddr, 1)
	if status[0] != 0 {
		t.Errorf("status = %d, want 0 (OK)", status[0])
	}
	if _, length := readUsedEntryAt(mem, blkUsed, 0); length != 1 {
		t.Errorf("used entry len = %d, want 1 (status byte only)", length)
	}
}

func TestBlkWriteRejectedWhenReadonly(t *testing.T) {
	backend := newFakeBlockBackend(make([]byte, 4096))
	b, mem := newTestBlk(t, backend, 4096, true)

	_, statusAddr := writeBlkRequest(mem, 1 /* VIRTIO_BLK_T_OUT */, 0, 4, false)
	copy(mem[blkDataAddr+0x100:], []byte("nope"))

	b.HandleNotify(0)

	status, _ := mem.Slice(statusAddr, 1)
	if status[0] != 1 { // VIRTIO_BLK_S_IOERR
		t.Errorf("status = %d, want 1 (IOERR)", status[0])
	}
	if bytes.Contains(backend.data, []byte("nope")) {
		t.Error("write landed on a readonly backend")
	}
}

func TestBlkFlush(t *testing.T) {
	backend := newFakeBlockBackend(make([]byte, 512))
	b, mem := newTestBlk(t, backend, 512, false)

	header := make([]byte, 16)
	binary.LittleEndian.PutUint32(header, 4) // VIRTIO_BLK_T_FLUSH
	copy(mem[blkDataAddr:], header)
	statusAddr := uint64(blkDataAddr + 0x200)
	writeDescAt(mem, blkDesc, 0, blkDataAddr, 16, 1, 1)
	writeDescAt(mem, blkDesc, 1, statusAddr, 1, 2, 0) // no data descriptor, just header -> status
	publishAvailAt(mem, blkAvail, 0, 0)

	b.HandleNotify(0)

	if backend.syncCalls != 1 {
		t.Errorf("Sync called %d times, want 1", backend.syncCalls)
	}
	status, _ := mem.Slice(statusAddr, 1)
	if status[0] != 0 {
		t.Errorf("status = %d, want 0 (OK)", status[0])
	}
}

func TestBlkUnsupportedRequestType(t *testing.T) {
	backend := newFakeBlockBackend(make([]byte, 512))
	b, mem := newTestBlk(t, backend, 512, false)

	_, statusAddr := writeBlkRequest(mem, 11 /* VIRTIO_BLK_T_DISCARD, unsupported */, 0, 16, true)

	b.HandleNotify(0)

	status, _ := mem.Slice(statusAddr, 1)
	if status[0] != 2 { // VIRTIO_BLK_S_UNSUPP
		t.Errorf("status = %d, want 2 (UNSUPP)", status[0])
	}
}

func TestBlkHandleNotifyIgnoresOtherQueueIndex(t *testing.T) {
	backend := newFakeBlockBackend(make([]byte, 512))
	b, mem := newTestBlk(t, backend, 512, false)
	writeBlkRequest(mem, 0, 0, 16, true)

	b.HandleNotify(1) // not blkQueueIndex: must not process anything

	if _, length := readUsedEntryAt(mem, blkUsed, 0); length != 0 {
		t.Error("HandleNotify(1) processed a request, want no-op")
	}
}
