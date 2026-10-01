// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package virtio

import (
	"encoding/binary"
	"io"
)

const (
	blkDeviceID = 2 // virtio 1.1 §5.2: block device

	blkQueueIndex = 0 // a single request queue — no VIRTIO_BLK_F_MQ offered

	// blkReqHeaderSize is sizeof(struct virtio_blk_req) minus its
	// trailing variable-length data and 1-byte status (virtio 1.1
	// §5.2.6.2): type le32, reserved le32, sector le64.
	blkReqHeaderSize = 16

	blkTypeIn    = 0 // VIRTIO_BLK_T_IN: read from the device
	blkTypeOut   = 1 // VIRTIO_BLK_T_OUT: write to the device
	blkTypeFlush = 4 // VIRTIO_BLK_T_FLUSH

	blkStatusOK     = 0
	blkStatusIOErr  = 1
	blkStatusUnsupp = 2

	sectorSize = 512
)

// BlockBackend is what a virtio-blk device reads/writes/flushes against —
// *os.File satisfies it directly. A separate interface (rather than just
// taking *os.File) keeps Blk unit-testable against an in-memory backend.
type BlockBackend interface {
	io.ReaderAt
	io.WriterAt
	Sync() error
}

// Blk is a virtio-blk device backed by a single raw disk file — the same
// convention internal/manager's vmImageCache/cloneFile already produce for
// cloud-hypervisor today (see docs/architecture/realization.md), so an
// existing microvm boot image needs no conversion to be usable here. No
// multi-queue, no discard/write-zeroes: genuine parity with what
// cloud-hypervisor's own chDiskConfig{Path, Readonly} exposes today, not a
// reduced target.
type Blk struct {
	transport   *Transport
	backend     BlockBackend
	readonly    bool
	mem         GuestMemory
	onInterrupt func()

	queue     *VirtQueue
	queueAddr uint64
}

// NewBlk creates a virtio-blk device of capacityBytes (rounded down to a
// whole number of 512-byte sectors, per the config space's own unit)
// backed by backend. onInterrupt is called, from whichever goroutine
// services a queue notify, whenever this device has pushed to its used
// ring and the guest should be told — mmio.go's dispatcher pulses the
// GSI, the same role it plays for Net.
func NewBlk(backend BlockBackend, capacityBytes int64, readonly bool, mem GuestMemory, onInterrupt func()) *Blk {
	config := make([]byte, 8)
	binary.LittleEndian.PutUint64(config, uint64(capacityBytes/sectorSize))

	b := &Blk{backend: backend, readonly: readonly, mem: mem, onInterrupt: onInterrupt}
	b.transport = NewTransport(blkDeviceID, 0, 1, 256, config)
	return b
}

// Transport returns the device's virtio-mmio register window, for
// registering with internal/hypervisor's MMIO dispatch.
func (b *Blk) Transport() *Transport {
	return b.transport
}

func (b *Blk) ensureQueue() {
	q := b.transport.Queue(blkQueueIndex)
	if q.Ready && q.Avail != b.queueAddr {
		b.queue = NewVirtQueue(b.mem, q)
		b.queueAddr = q.Avail
	}
}

// HandleNotify is the onNotify callback for internal/hypervisor's MMIO
// dispatch (registerMMIODevice): drains the request queue, answering each
// request synchronously (blocking file I/O is acceptable here — there is
// exactly one request queue and this is called from a vCPU's own
// goroutine servicing its own exit, the same way Net.HandleNotify already
// blocks on a tap write).
func (b *Blk) HandleNotify(queueIdx uint32) {
	if queueIdx != blkQueueIndex {
		return
	}
	b.ensureQueue()
	if b.queue == nil {
		return
	}

	handled := false
	for {
		chain, ok, err := b.queue.Pop()
		if err != nil || !ok {
			break
		}
		written := b.handleRequest(chain)
		if err := b.queue.Push(chain.HeadIndex, written); err != nil {
			break
		}
		handled = true
	}
	if handled {
		b.transport.RaiseUsedBufferInterrupt()
		if b.onInterrupt != nil {
			b.onInterrupt()
		}
	}
}

// handleRequest answers one request: Segments[0] is the read-only header,
// Segments[len-1] the writable 1-byte status, anything between is the
// read or write data (virtio 1.1 §5.2.6.2). It returns how many bytes
// were written into writable segments, for Push's used-ring length.
func (b *Blk) handleRequest(chain *Chain) uint32 {
	if len(chain.Segments) < 2 {
		return 0 // malformed: not even room for a header and a status byte
	}
	header := chain.Segments[0]
	status := chain.Segments[len(chain.Segments)-1]
	data := chain.Segments[1 : len(chain.Segments)-1]

	if len(header.Data) < blkReqHeaderSize || len(status.Data) < 1 {
		return 0
	}
	reqType := binary.LittleEndian.Uint32(header.Data[0:4])
	sector := binary.LittleEndian.Uint64(header.Data[8:16])
	offset := int64(sector) * sectorSize

	statusCode, written := b.execute(reqType, offset, data)
	status.Data[0] = statusCode
	return written + 1 // +1 for the status byte itself
}

func (b *Blk) execute(reqType uint32, offset int64, data []Segment) (statusCode byte, written uint32) {
	switch reqType {
	case blkTypeIn:
		for _, s := range data {
			n, err := b.backend.ReadAt(s.Data, offset)
			written += uint32(n)
			offset += int64(n)
			if err != nil && err != io.EOF {
				return blkStatusIOErr, written
			}
		}
		return blkStatusOK, written

	case blkTypeOut:
		if b.readonly {
			return blkStatusIOErr, 0
		}
		for _, s := range data {
			n, err := b.backend.WriteAt(s.Data, offset)
			offset += int64(n)
			if err != nil {
				return blkStatusIOErr, 0
			}
		}
		return blkStatusOK, 0

	case blkTypeFlush:
		if err := b.backend.Sync(); err != nil {
			return blkStatusIOErr, 0
		}
		return blkStatusOK, 0

	default:
		return blkStatusUnsupp, 0
	}
}
