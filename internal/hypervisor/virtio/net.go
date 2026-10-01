// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package virtio

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sync"
)

const (
	netDeviceID = 1 // virtio 1.1 §5.1: network card

	netFeatureMAC = 1 << 5 // VIRTIO_NET_F_MAC: use Config's mac instead of a driver-generated one

	netQueueRX = 0 // receiveq, by convention queue 0 before any VIRTIO_NET_F_MQ negotiation
	netQueueTX = 1 // transmitq

	// netHdrSize is sizeof(struct virtio_net_hdr) (virtio 1.1 §5.1.6.1):
	// flags u8, gso_type u8, hdr_len le16, gso_size le16, csum_start
	// le16, csum_offset le16 — the legacy, 10-byte header, not the
	// 12-byte virtio_net_hdr_mrg_rxbuf variant, since this device never
	// offers VIRTIO_NET_F_MRG_RXBUF. Every field stays zero: no
	// VIRTIO_NET_F_* offload (checksum, TSO/GSO) is offered either, so
	// neither side ever has anything meaningful to put in it.
	netHdrSize = 10

	// maxFrameSize bounds one read from the tap device — generous for a
	// standard (non-jumbo, non-TSO) Ethernet frame plus headroom, not a
	// protocol requirement.
	maxFrameSize = 65536
)

// Net is a virtio-net device backed by an already-open TAP file descriptor
// (OpenTap) for frame I/O. Config space is just a MAC and a permanently
// "up" link status — minimal, but genuine parity with what microvm's
// cloud-hypervisor backend used to configure via its own net.tap-by-name
// attachment: a single network interface, no offload features, no
// multiqueue.
type Net struct {
	transport   *Transport
	tap         io.ReadWriter
	mem         GuestMemory
	onInterrupt func()

	mu          sync.Mutex
	rxQueue     *VirtQueue
	txQueue     *VirtQueue
	rxQueueAddr uint64 // Avail address the cached rxQueue was built from, to detect a re-negotiation
	txQueueAddr uint64
}

// NewNet creates a virtio-net device. tap is the device's frame transport
// (OpenTap's result in production, a fake in tests); mem resolves the
// guest-memory addresses its virtqueues reference. onInterrupt is called,
// from whichever goroutine triggers it (ReadLoop for an inbound frame, or
// whatever goroutine services a TX queue notify), whenever this device has
// pushed to a used ring and the guest should be told — pulsing the
// device's GSI is internal/hypervisor's job, not this package's, since
// Transport/Net have no KVM handle to do it themselves.
func NewNet(mac [6]byte, tap io.ReadWriter, mem GuestMemory, onInterrupt func()) *Net {
	config := make([]byte, 8)
	copy(config[0:6], mac[:])
	binary.LittleEndian.PutUint16(config[6:8], 1) // status: VIRTIO_NET_S_LINK_UP

	n := &Net{
		tap:         tap,
		mem:         mem,
		onInterrupt: onInterrupt,
	}
	n.transport = NewTransport(netDeviceID, netFeatureMAC, 2, 256, config)
	return n
}

// Transport returns the device's virtio-mmio register window, for
// registering with internal/hypervisor's MMIO dispatch.
func (n *Net) Transport() *Transport {
	return n.transport
}

// ensureQueues (re)builds the cached VirtQueue wrappers once the driver
// has configured and marked ready the corresponding transport queue —
// lazily, since there is no single well-defined moment during device
// setup to do this eagerly (the driver notifies queue 0's readiness no
// differently than any other register write).
func (n *Net) ensureQueues() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if rx := n.transport.Queue(netQueueRX); rx.Ready && rx.Avail != n.rxQueueAddr {
		n.rxQueue = NewVirtQueue(n.mem, rx)
		n.rxQueueAddr = rx.Avail
	}
	if tx := n.transport.Queue(netQueueTX); tx.Ready && tx.Avail != n.txQueueAddr {
		n.txQueue = NewVirtQueue(n.mem, tx)
		n.txQueueAddr = tx.Avail
	}
}

// HandleNotify is the onNotify callback for internal/hypervisor's MMIO
// dispatch (registerMMIODevice): queueIdx netQueueTX drains the transmit
// queue into the tap device; any other index (netQueueRX included — there
// is nothing to do when the driver merely adds a receive buffer, since
// delivery is driven by ReadLoop, not by this notification) is a no-op.
func (n *Net) HandleNotify(queueIdx uint32) {
	if queueIdx != netQueueTX {
		return
	}
	n.ensureQueues()

	n.mu.Lock()
	txQueue := n.txQueue
	n.mu.Unlock()
	if txQueue == nil {
		return
	}

	sent := false
	for {
		chain, ok, err := txQueue.Pop()
		if err != nil || !ok {
			break
		}
		if err := n.transmit(chain); err == nil {
			sent = true
		}
		if err := txQueue.Push(chain.HeadIndex, 0); err != nil {
			break
		}
	}
	if sent {
		n.transport.RaiseUsedBufferInterrupt()
		if n.onInterrupt != nil {
			n.onInterrupt()
		}
	}
}

// transmit concatenates chain's segments (the driver never splits the
// virtio_net_hdr from the frame payload in a way this needs to care
// about — whatever the descriptor boundaries are, netHdrSize bytes of
// header come first) and writes the frame past the header to the tap
// device.
func (n *Net) transmit(chain *Chain) error {
	total := 0
	for _, s := range chain.Segments {
		total += len(s.Data)
	}
	if total < netHdrSize {
		return fmt.Errorf("virtio: tx descriptor chain is %d bytes, shorter than the %d-byte virtio_net_hdr", total, netHdrSize)
	}

	buf := make([]byte, 0, total)
	for _, s := range chain.Segments {
		buf = append(buf, s.Data...)
	}
	_, err := n.tap.Write(buf[netHdrSize:])
	return err
}

// ReadLoop reads frames from the tap device and delivers each to the next
// available RX buffer, dropping it if the driver hasn't supplied one (the
// guest, not this device, owns retransmission/loss recovery — the same
// tradeoff any real NIC under memory pressure makes). It returns when ctx
// is done, or on an unrecoverable tap read error.
func (n *Net) ReadLoop(ctx context.Context) error {
	frame := make([]byte, maxFrameSize)
	for {
		if ctx.Err() != nil {
			return nil
		}
		nRead, err := n.tap.Read(frame)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) || errors.Is(err, fs.ErrClosed) {
				// The tap fd was closed to unblock this read, either
				// as part of an orderly shutdown (ctx already
				// cancelled by the time Close reaches here, in
				// cmd/hypervisor's handleShutdown) or by a caller
				// calling Machine.Close directly without cancelling
				// ctx first (fs.ErrClosed, not io.EOF, is what a read
				// on an already-closed *os.File actually returns) —
				// neither is this device's error to report.
				return nil
			}
			return fmt.Errorf("virtio: read tap: %w", err)
		}
		n.deliver(frame[:nRead])
	}
}

func (n *Net) deliver(frameData []byte) {
	n.ensureQueues()
	n.mu.Lock()
	rxQueue := n.rxQueue
	n.mu.Unlock()
	if rxQueue == nil {
		return // no driver-supplied rx buffers yet; drop
	}

	chain, ok, err := rxQueue.Pop()
	if err != nil || !ok {
		return // no buffer available right now; drop
	}

	written, err := writeAcross(chain.Segments, netHeader(), frameData)
	if err != nil {
		return
	}
	if err := rxQueue.Push(chain.HeadIndex, uint32(written)); err != nil { // #nosec G115 -- written is bounded by the rx chain's own writable segment lengths (driver-allocated buffers, realistically a few KiB at most), never negative
		return
	}
	n.transport.RaiseUsedBufferInterrupt()
	if n.onInterrupt != nil {
		n.onInterrupt()
	}
}

// netHeader returns a zeroed virtio_net_hdr — see the netHdrSize comment
// for why every field is legitimately always zero for this device.
func netHeader() []byte {
	return make([]byte, netHdrSize)
}

// writeAcross copies header then data, in order, across segments'
// writable portions (skipping any non-writable segment — a well-formed rx
// descriptor chain shouldn't have one, but this does not assume it
// doesn't), returning how many bytes were written in total. It errors if
// the chain doesn't have enough writable room for all of it.
func writeAcross(segments []Segment, header, data []byte) (int, error) {
	remaining := append(append([]byte(nil), header...), data...)
	written := 0
	for _, s := range segments {
		if !s.Writable || len(remaining) == 0 {
			continue
		}
		n := copy(s.Data, remaining)
		remaining = remaining[n:]
		written += n
	}
	if len(remaining) != 0 {
		return written, fmt.Errorf("virtio: rx descriptor chain has no room for %d more bytes", len(remaining))
	}
	return written, nil
}
