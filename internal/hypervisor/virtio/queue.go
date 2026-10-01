// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package virtio

import (
	"encoding/binary"
	"fmt"
	"sync"
)

// GuestMemory abstracts access to guest-physical memory: Slice returns the
// live backing bytes at a guest address (no copy — writes through the
// returned slice are visible to the guest immediately), the same role
// internal/hypervisor's Machine.write plays for one-shot boot-time writes,
// but readable too and usable from the run loop. Letting VirtQueue and
// every device in this package depend on this interface rather than a
// concrete KVM-backed type is what makes them unit-testable without real
// /dev/kvm memory — tests supply a plain []byte-backed fake.
type GuestMemory interface {
	Slice(addr uint64, length int) ([]byte, error)
}

// Split virtqueue layout (virtio 1.1 §2.6): a fixed-size descriptor
// (virtq_desc), and the avail/used rings' header and per-entry sizes.
// None of this is negotiable or CPU-native-alignment-sensitive — the spec
// mandates these exact byte widths regardless of host or guest struct
// packing conventions.
const (
	descSize      = 16 // addr u64 + len u32 + flags u16 + next u16
	descFlagNext  = 1 << 0
	descFlagWrite = 1 << 1

	availHeaderSize = 4 // flags u16 + idx u16
	availEntrySize  = 2 // u16 descriptor-chain head index

	usedHeaderSize = 4 // flags u16 + idx u16
	usedEntrySize  = 8 // id u32 + len u32

	// maxChainLength bounds descriptor-chain walking against a
	// cyclic or absurdly long chain a buggy or malicious guest could
	// construct — real chains are a handful of descriptors at most.
	maxChainLength = 1024
)

// Segment is one descriptor's worth of a Chain: Data is the live guest
// memory it points to (via GuestMemory.Slice, so writing into it for a
// Writable segment is visible to the guest), Writable is whether the
// device may write into it (VIRTQ_DESC_F_WRITE) versus must only read it.
type Segment struct {
	Data     []byte
	Writable bool
}

// Chain is one request: the descriptor chain a driver placed in the avail
// ring, walked and resolved to live memory segments. HeadIndex is what
// Push must be called with once the device has handled it.
type Chain struct {
	HeadIndex uint16
	Segments  []Segment
}

// TotalWritableLen sums the writable segments' lengths — how much
// "response" room a device has to write into across the whole chain
// (e.g. virtio-blk's data-in buffer plus its trailing status byte may be
// separate descriptors).
func (c *Chain) TotalWritableLen() int {
	n := 0
	for _, s := range c.Segments {
		if s.Writable {
			n += len(s.Data)
		}
	}
	return n
}

// VirtQueue processes one virtqueue's avail/used rings against live guest
// memory. It is safe for concurrent use, serializing Pop/Push against each
// other — not because the spec requires it, but because this package
// makes no assumption about which vCPU's goroutine ends up servicing a
// given QueueNotify.
type VirtQueue struct {
	mem                           GuestMemory
	size                          uint16
	descAddr, availAddr, usedAddr uint64

	mu           sync.Mutex
	lastAvailIdx uint16
}

// NewVirtQueue wraps q's ring addresses (from Transport.Queue, once
// q.Ready) for processing against mem. A queue's addresses are fixed once
// the driver sets QueueReady, so a snapshot taken then stays valid for the
// queue's lifetime — there is no need to re-read Transport on every Pop.
func NewVirtQueue(mem GuestMemory, q Queue) *VirtQueue {
	return &VirtQueue{mem: mem, size: uint16(q.Size), descAddr: q.Desc, availAddr: q.Avail, usedAddr: q.Used} // #nosec G115 -- q.Size is bounded to [1, queueMaxSize] by Transport.Write's regQueueNum handler, and every queueMaxSize this package's devices pass to NewTransport fits in uint16
}

// Pop returns the next request the driver has made available, if any
// (ok is false and err is nil when the driver has nothing new). The
// descriptor chain is fully resolved to live memory segments.
func (vq *VirtQueue) Pop() (chain *Chain, ok bool, err error) {
	vq.mu.Lock()
	defer vq.mu.Unlock()

	availIdx, err := vq.readAvailIdx()
	if err != nil {
		return nil, false, err
	}
	if availIdx == vq.lastAvailIdx {
		return nil, false, nil
	}

	entryOffset := vq.availAddr + availHeaderSize + uint64(vq.lastAvailIdx%vq.size)*availEntrySize
	entry, err := vq.mem.Slice(entryOffset, availEntrySize)
	if err != nil {
		return nil, false, fmt.Errorf("virtio: read avail ring entry: %w", err)
	}
	headIdx := binary.LittleEndian.Uint16(entry)

	c, err := vq.readChain(headIdx)
	if err != nil {
		return nil, false, err
	}
	vq.lastAvailIdx++
	return c, true, nil
}

func (vq *VirtQueue) readAvailIdx() (uint16, error) {
	b, err := vq.mem.Slice(vq.availAddr+2, 2) // offset 2: idx, past the flags field
	if err != nil {
		return 0, fmt.Errorf("virtio: read avail.idx: %w", err)
	}
	return binary.LittleEndian.Uint16(b), nil
}

func (vq *VirtQueue) readChain(headIdx uint16) (*Chain, error) {
	c := &Chain{HeadIndex: headIdx}
	idx := headIdx
	for i := 0; i < maxChainLength; i++ {
		d, err := vq.mem.Slice(vq.descAddr+uint64(idx)*descSize, descSize)
		if err != nil {
			return nil, fmt.Errorf("virtio: read descriptor %d: %w", idx, err)
		}
		addr := binary.LittleEndian.Uint64(d[0:])
		length := binary.LittleEndian.Uint32(d[8:])
		flags := binary.LittleEndian.Uint16(d[12:])
		next := binary.LittleEndian.Uint16(d[14:])

		data, err := vq.mem.Slice(addr, int(length))
		if err != nil {
			return nil, fmt.Errorf("virtio: descriptor %d data (addr=0x%x len=%d): %w", idx, addr, length, err)
		}
		c.Segments = append(c.Segments, Segment{Data: data, Writable: flags&descFlagWrite != 0})

		if flags&descFlagNext == 0 {
			return c, nil
		}
		idx = next
	}
	return nil, fmt.Errorf("virtio: descriptor chain starting at %d exceeds %d entries (cyclic?)", headIdx, maxChainLength)
}

// Push publishes a used-ring entry for the chain Pop returned as
// headIndex, recording writtenLen bytes written into it, and advances
// used.idx so the driver sees it. The entry is written before idx is
// advanced, in that order, relying on x86-64's total-store-order
// guarantee (stores are never reordered relative to other stores) for the
// driver — running on a real, different CPU from whichever vCPU's
// goroutine calls this — to never observe the new idx before the entry
// data it points to.
func (vq *VirtQueue) Push(headIndex uint16, writtenLen uint32) error {
	vq.mu.Lock()
	defer vq.mu.Unlock()

	usedIdx, err := vq.readUsedIdx()
	if err != nil {
		return err
	}

	entryOffset := vq.usedAddr + usedHeaderSize + uint64(usedIdx%vq.size)*usedEntrySize
	entry, err := vq.mem.Slice(entryOffset, usedEntrySize)
	if err != nil {
		return fmt.Errorf("virtio: write used ring entry: %w", err)
	}
	binary.LittleEndian.PutUint32(entry[0:], uint32(headIndex))
	binary.LittleEndian.PutUint32(entry[4:], writtenLen)

	idxBytes, err := vq.mem.Slice(vq.usedAddr+2, 2)
	if err != nil {
		return fmt.Errorf("virtio: write used.idx: %w", err)
	}
	binary.LittleEndian.PutUint16(idxBytes, usedIdx+1)
	return nil
}

func (vq *VirtQueue) readUsedIdx() (uint16, error) {
	b, err := vq.mem.Slice(vq.usedAddr+2, 2)
	if err != nil {
		return 0, fmt.Errorf("virtio: read used.idx: %w", err)
	}
	return binary.LittleEndian.Uint16(b), nil
}
