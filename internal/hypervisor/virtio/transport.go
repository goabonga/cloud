// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package virtio implements the virtio-mmio transport (virtio 1.1 §4.2) and
// the devices built on it (virtio-net, virtio-blk). virtio-mmio, not
// virtio-pci, is the deliberate choice for this hypervisor: it needs no PCI
// bus, config-space or BAR emulation at all — each device is just a flat
// MMIO register window the guest's virtio_mmio.device= cmdline parameter
// tells it where to find, the same minimal-VMM precedent Firecracker set.
//
// Transport in this file owns every register virtio-mmio defines
// (status/feature-negotiation state machine, per-queue address/size/ready
// state) independent of which concrete device sits on top of it; it has no
// KVM or guest-memory knowledge; internal/hypervisor maps a Transport's
// register window onto a KVM_EXIT_MMIO address range.
package virtio

import (
	"encoding/binary"
	"sync"
)

// Register byte offsets (virtio 1.1 §4.2.2), all accessed as the guest's
// virtio_mmio.c driver does: readl/writel, i.e. 4-byte little-endian,
// except within the device-specific config space, which can be any width.
const (
	regMagicValue        = 0x000 // R
	regVersion           = 0x004 // R
	regDeviceID          = 0x008 // R
	regVendorID          = 0x00c // R
	regDeviceFeatures    = 0x010 // R, paged by regDeviceFeaturesSel
	regDeviceFeaturesSel = 0x014 // W
	regDriverFeatures    = 0x020 // W, paged by regDriverFeaturesSel
	regDriverFeaturesSel = 0x024 // W
	regQueueSel          = 0x030 // W
	regQueueNumMax       = 0x034 // R
	regQueueNum          = 0x038 // W
	regQueueReady        = 0x044 // RW
	regInterruptStatus   = 0x060 // R
	regInterruptACK      = 0x064 // W
	regStatus            = 0x070 // RW; writing 0 resets the device
	regQueueDescLow      = 0x080 // W
	regQueueDescHigh     = 0x084 // W
	regQueueDriverLow    = 0x090 // W (the avail ring)
	regQueueDriverHigh   = 0x094 // W
	regQueueDeviceLow    = 0x0a0 // W (the used ring)
	regQueueDeviceHigh   = 0x0a4 // W
	regConfigGeneration  = 0x0fc // R

	// ConfigSpaceOffset is where device-specific config (ReadConfig)
	// starts; registers below it are the transport's own.
	ConfigSpaceOffset = 0x100

	// QueueNotifyOffset is where a guest write means "check virtqueue
	// QueueNotifyData(data) for new requests" — the one transport
	// register Transport itself doesn't handle (see Write's doc
	// comment): the caller dispatches it to a device, which Transport
	// has no notion of. Exported so that dispatcher can special-case it.
	QueueNotifyOffset = 0x050
)

// QueueNotifyData decodes a QueueNotifyOffset write's payload: the index
// of the virtqueue the driver wants checked.
func QueueNotifyData(data []byte) uint32 {
	return binary.LittleEndian.Uint32(data)
}

const (
	magicValue = 0x74726976 // "virt", little-endian
	version    = 2          // non-legacy (modern) virtio-mmio
)

// Device status bits (virtio 1.1 §2.1).
const (
	StatusAcknowledge      = 1 << 0
	StatusDriver           = 1 << 1
	StatusDriverOK         = 1 << 2
	StatusFeaturesOK       = 1 << 3
	StatusDeviceNeedsReset = 1 << 6
	StatusFailed           = 1 << 7
)

// UsedBufferInterrupt is InterruptStatus bit 0 (virtio 1.1 §4.2.2.3),
// signalling a used-ring update; bit 1 (a device-config change) is unused
// — none of this package's devices mutate their own config after creation.
const UsedBufferInterrupt = 1 << 0

// Queue is one virtqueue's transport-level state: its negotiated size and
// the three ring addresses the driver wrote, valid once Ready is true.
type Queue struct {
	Size  uint32
	Ready bool
	Desc  uint64 // descriptor table
	Avail uint64 // "QueueDriver" in the spec's modern naming
	Used  uint64 // "QueueDevice" in the spec's modern naming
}

// Transport is one device's virtio-mmio register window.
type Transport struct {
	deviceID       uint32
	deviceFeatures uint64 // what this device offers; VIRTIO_F_VERSION_1 (bit 32) is added automatically, required for non-legacy mode
	numQueues      int
	queueMaxSize   uint32 // the same max for every queue — fine for this package's devices (net: 2 queues, blk: 1)
	config         []byte // device-specific config space, read-only to the guest

	mu                sync.Mutex
	status            uint8
	deviceFeaturesSel uint32
	driverFeaturesSel uint32
	driverFeatures    uint64
	queueSel          uint32
	queues            []Queue
	interruptStatus   uint32
}

// NewTransport creates a device's register window. deviceID is a
// virtio_mmio DeviceID (1 = network, 2 = block); deviceFeatures are the
// feature bits beyond VIRTIO_F_VERSION_1 the device offers; numQueues and
// queueMaxSize bound how many virtqueues the driver may configure and how
// large each may be; config is the device-specific config space
// ReadConfig serves starting at ConfigSpaceOffset.
func NewTransport(deviceID uint32, deviceFeatures uint64, numQueues int, queueMaxSize uint32, config []byte) *Transport {
	const versionOneBit = 1 << 32
	t := &Transport{
		deviceID:       deviceID,
		deviceFeatures: deviceFeatures | versionOneBit,
		numQueues:      numQueues,
		queueMaxSize:   queueMaxSize,
		config:         config,
	}
	t.resetLocked()
	return t
}

// resetLocked restores every negotiable/driver-set register to its
// power-on default (virtio 1.1 §2.1's device status writing 0), called
// both from NewTransport and from a guest writing 0 to the status
// register. numQueues/queueMaxSize/config/deviceFeatures are fixed at
// construction and untouched by a reset.
func (t *Transport) resetLocked() {
	t.status = 0
	t.deviceFeaturesSel = 0
	t.driverFeaturesSel = 0
	t.driverFeatures = 0
	t.queueSel = 0
	t.queues = make([]Queue, t.numQueues)
	t.interruptStatus = 0
}

// selectedQueue returns the currently QueueSel-selected queue, or nil if
// the driver selected an index beyond numQueues (a driver bug/malicious
// guest this transport tolerates rather than panics on). Callers must
// hold t.mu.
func (t *Transport) selectedQueue() *Queue {
	if int(t.queueSel) >= len(t.queues) {
		return nil
	}
	return &t.queues[t.queueSel]
}

// featuresPage returns the 32-bit page (bits 0-31 for sel=0, 32-63 for
// sel=1) of a 64-bit feature bitmap — how DeviceFeatures/DriverFeatures
// expose more than 32 feature bits through a 32-bit register (virtio 1.1
// §4.2.2.2's FeaturesSel mechanism). Any other sel is reserved/undefined;
// this returns 0 for it rather than guessing.
func featuresPage(features uint64, sel uint32) uint32 {
	switch sel {
	case 0:
		return uint32(features)
	case 1:
		return uint32(features >> 32)
	default:
		return 0
	}
}

// readConfig copies len(data) bytes of src starting at offset into data,
// zero-filling whatever falls past the end of src — a guest reading past
// a device's declared config space gets zeros, not a crash.
func readConfig(src []byte, offset uint64, data []byte) {
	for i := range data {
		data[i] = 0
	}
	if offset >= uint64(len(src)) {
		return
	}
	copy(data, src[offset:])
}

// Read answers a guest read of len(data) bytes at offset (relative to this
// device's MMIO window base), filling data.
func (t *Transport) Read(offset uint64, data []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if offset >= ConfigSpaceOffset {
		readConfig(t.config, offset-ConfigSpaceOffset, data)
		return
	}
	if len(data) != 4 {
		return // every transport register is accessed as a 4-byte readl; anything else has nothing meaningful to return
	}

	var v uint32
	switch offset {
	case regMagicValue:
		v = magicValue
	case regVersion:
		v = version
	case regDeviceID:
		v = t.deviceID
	case regVendorID:
		v = 0
	case regDeviceFeatures:
		v = featuresPage(t.deviceFeatures, t.deviceFeaturesSel)
	case regQueueNumMax:
		v = t.queueMaxSize
	case regQueueReady:
		if q := t.selectedQueue(); q != nil && q.Ready {
			v = 1
		}
	case regInterruptStatus:
		v = t.interruptStatus
	case regStatus:
		v = uint32(t.status)
	case regConfigGeneration:
		v = 0 // this package's devices never change their config space after creation
	}
	binary.LittleEndian.PutUint32(data, v)
}

// Write answers a guest write of data's bytes at offset. It does not
// handle regQueueNotify (offset 0x050): the caller dispatches that to the
// device, since Transport has no notion of what a queue kick should do.
func (t *Transport) Write(offset uint64, data []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if offset >= ConfigSpaceOffset {
		return // this package's device config spaces are all read-only
	}
	if len(data) != 4 {
		return
	}
	v := binary.LittleEndian.Uint32(data)

	switch offset {
	case regDeviceFeaturesSel:
		t.deviceFeaturesSel = v
	case regDriverFeaturesSel:
		t.driverFeaturesSel = v
	case regDriverFeatures:
		if t.driverFeaturesSel == 0 {
			t.driverFeatures = t.driverFeatures&0xffffffff00000000 | uint64(v)
		} else {
			t.driverFeatures = t.driverFeatures&0x00000000ffffffff | uint64(v)<<32
		}
	case regQueueSel:
		t.queueSel = v
	case regQueueNum:
		// A driver writing 0 or more than the advertised QueueNumMax is a
		// spec violation (virtio 1.1 §4.2.2.3); ignored rather than stored,
		// so a later NewVirtQueue never sees a size that over/underflows
		// uint16 or divides-by-zero on vq.size.
		if q := t.selectedQueue(); q != nil && v > 0 && v <= t.queueMaxSize {
			q.Size = v
		}
	case regQueueReady:
		if q := t.selectedQueue(); q != nil {
			q.Ready = v&1 != 0
		}
	case regInterruptACK:
		t.interruptStatus &^= v
	case regStatus:
		if v == 0 {
			t.resetLocked()
			return
		}
		t.status = uint8(v)
	case regQueueDescLow:
		if q := t.selectedQueue(); q != nil {
			q.Desc = q.Desc&0xffffffff00000000 | uint64(v)
		}
	case regQueueDescHigh:
		if q := t.selectedQueue(); q != nil {
			q.Desc = q.Desc&0x00000000ffffffff | uint64(v)<<32
		}
	case regQueueDriverLow:
		if q := t.selectedQueue(); q != nil {
			q.Avail = q.Avail&0xffffffff00000000 | uint64(v)
		}
	case regQueueDriverHigh:
		if q := t.selectedQueue(); q != nil {
			q.Avail = q.Avail&0x00000000ffffffff | uint64(v)<<32
		}
	case regQueueDeviceLow:
		if q := t.selectedQueue(); q != nil {
			q.Used = q.Used&0xffffffff00000000 | uint64(v)
		}
	case regQueueDeviceHigh:
		if q := t.selectedQueue(); q != nil {
			q.Used = q.Used&0x00000000ffffffff | uint64(v)<<32
		}
	}
}

// IsDriverOK reports whether the driver has finished initialization
// (virtio 1.1 §2.1's DRIVER_OK status bit) — a device should not process
// virtqueues before this, even if ready/address registers happen to be
// set, since the driver isn't done configuring yet.
func (t *Transport) IsDriverOK() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status&StatusDriverOK != 0
}

// DriverFeatures returns the feature bits the driver acked (written via
// regDriverFeatures), valid once IsDriverOK is true.
func (t *Transport) DriverFeatures() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.driverFeatures
}

// Queue returns a snapshot of virtqueue idx's transport-level state (size,
// ready, ring addresses) for the device/virtqueue-processing code to act
// on. The zero Queue (not Ready) for an out-of-range idx.
func (t *Transport) Queue(idx int) Queue {
	t.mu.Lock()
	defer t.mu.Unlock()
	if idx < 0 || idx >= len(t.queues) {
		return Queue{}
	}
	return t.queues[idx]
}

// RaiseUsedBufferInterrupt marks InterruptStatus so the next guest read of
// it reports a used-ring update. The caller is still responsible for
// actually asserting the device's GSI (console.go's pulseIRQ does the
// analogous thing for the UART) — Transport has no KVM handle to do that
// itself.
func (t *Transport) RaiseUsedBufferInterrupt() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.interruptStatus |= UsedBufferInterrupt
}
