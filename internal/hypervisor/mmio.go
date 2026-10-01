// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package hypervisor

import (
	"fmt"

	"github.com/goabonga/infrastructure/internal/hypervisor/boot"
	"github.com/goabonga/infrastructure/internal/hypervisor/virtio"
)

// mmioDevice is one registered virtio-mmio device: its register window and
// the pieces Transport itself doesn't know how to do (see
// virtio.Transport.Write's doc comment on QueueNotifyOffset) — checking a
// virtqueue for new requests and asserting the device's interrupt line.
type mmioDevice struct {
	base, size uint64
	transport  *virtio.Transport
	gsi        uint32
	// onNotify handles a QueueNotifyOffset write: queueIdx is the
	// virtqueue the driver wants checked. It is this device's (not
	// Transport's, not Machine's) job to decide what that means — e.g.
	// virtio-net processing its TX queue — and to call
	// Transport.RaiseUsedBufferInterrupt plus pulseIRQ once it has.
	onNotify func(queueIdx uint32)
}

// registerMMIODevice assigns the next free MMIO window (boot.MMIOBaseAddr,
// spaced boot.MMIODeviceSize apart) to a new device and returns its base
// address and legacy-ISA-range-adjacent GSI (16 + however many devices
// were already registered — GSIs 0-15 are the default PIC/COM1 routing
// from New, GSI 16-23 the rest of the default IOAPIC's identity routing,
// already live with no KVM_SET_GSI_ROUTING call needed: see
// docs/architecture/go-hypervisor.md). It must be called during New,
// before Run — the MMIO dispatch table isn't safe to mutate concurrently
// with a running vCPU.
func (m *Machine) registerMMIODevice(transport *virtio.Transport, onNotify func(queueIdx uint32)) (baseAddr uint64, gsi uint32, err error) {
	const maxDevices = 8 // GSI 16-23: the rest of the default IOAPIC's 24 pins, a known scaling boundary noted in the plan this effort follows
	if len(m.mmioDevices) >= maxDevices {
		return 0, 0, fmt.Errorf("hypervisor: too many mmio devices (max %d, limited by the default IOAPIC's free GSIs)", maxDevices)
	}
	idx := len(m.mmioDevices)
	base := boot.MMIOBaseAddr + uint64(idx)*boot.MMIODeviceSize
	gsi = 16 + uint32(idx)
	m.mmioDevices = append(m.mmioDevices, &mmioDevice{
		base: base, size: boot.MMIODeviceSize, transport: transport, gsi: gsi, onNotify: onNotify,
	})
	return base, gsi, nil
}

// serveMMIO answers a KVM_EXIT_MMIO access if addr falls inside a
// registered device's window, reporting whether it did (an address
// outside every window is still relayed to Run's onExit callback). data
// is Run.MMIO's data slice: for a write it holds what the guest wrote,
// for a read this method fills it with what the guest should read.
func (m *Machine) serveMMIO(addr uint64, data []byte, isWrite bool) bool {
	for _, d := range m.mmioDevices {
		if addr < d.base || addr >= d.base+d.size {
			continue
		}
		off := addr - d.base
		switch {
		case !isWrite:
			d.transport.Read(off, data)
		case off == virtio.QueueNotifyOffset:
			if d.onNotify != nil {
				d.onNotify(virtio.QueueNotifyData(data))
			}
		default:
			d.transport.Write(off, data)
		}
		return true
	}
	return false
}
