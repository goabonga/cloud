// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package hypervisor

import (
	"encoding/binary"
	"testing"

	"github.com/goabonga/infrastructure/internal/hypervisor/boot"
	"github.com/goabonga/infrastructure/internal/hypervisor/virtio"
)

func TestRegisterMMIODeviceAssignsSequentialAddresses(t *testing.T) {
	m := &Machine{}
	base1, gsi1, err := m.registerMMIODevice(virtio.NewTransport(1, 0, 1, 1, nil), nil)
	if err != nil {
		t.Fatalf("registerMMIODevice (1st): %v", err)
	}
	base2, gsi2, err := m.registerMMIODevice(virtio.NewTransport(2, 0, 1, 1, nil), nil)
	if err != nil {
		t.Fatalf("registerMMIODevice (2nd): %v", err)
	}

	if base1 != boot.MMIOBaseAddr {
		t.Errorf("base1 = 0x%x, want 0x%x (MMIOBaseAddr)", base1, boot.MMIOBaseAddr)
	}
	if base2 != boot.MMIOBaseAddr+boot.MMIODeviceSize {
		t.Errorf("base2 = 0x%x, want 0x%x", base2, boot.MMIOBaseAddr+boot.MMIODeviceSize)
	}
	if gsi1 != 16 || gsi2 != 17 {
		t.Errorf("gsi1=%d gsi2=%d, want 16,17", gsi1, gsi2)
	}
}

func TestRegisterMMIODeviceRejectsTooMany(t *testing.T) {
	m := &Machine{}
	for i := 0; i < 8; i++ {
		if _, _, err := m.registerMMIODevice(virtio.NewTransport(1, 0, 1, 1, nil), nil); err != nil {
			t.Fatalf("registerMMIODevice (%d): %v", i, err)
		}
	}
	if _, _, err := m.registerMMIODevice(virtio.NewTransport(1, 0, 1, 1, nil), nil); err == nil {
		t.Fatal("registerMMIODevice succeeded past the 8-device cap, want error")
	}
}

func TestServeMMIODispatchesToCorrectDevice(t *testing.T) {
	m := &Machine{}
	base1, _, _ := m.registerMMIODevice(virtio.NewTransport(1 /* net */, 0, 1, 1, nil), nil)
	base2, _, _ := m.registerMMIODevice(virtio.NewTransport(2 /* blk */, 0, 1, 1, nil), nil)

	readDeviceID := func(base uint64) uint32 {
		data := make([]byte, 4)
		if !m.serveMMIO(base+0x008, data, false) {
			t.Fatalf("serveMMIO(0x%x) returned false, want true (handled)", base+0x008)
		}
		return binary.LittleEndian.Uint32(data)
	}
	if got := readDeviceID(base1); got != 1 {
		t.Errorf("device 1's DeviceID register = %d, want 1", got)
	}
	if got := readDeviceID(base2); got != 2 {
		t.Errorf("device 2's DeviceID register = %d, want 2", got)
	}
}

func TestServeMMIOFallsThroughForUnknownAddress(t *testing.T) {
	m := &Machine{}
	if _, _, err := m.registerMMIODevice(virtio.NewTransport(1, 0, 1, 1, nil), nil); err != nil {
		t.Fatalf("registerMMIODevice: %v", err)
	}

	data := make([]byte, 4)
	if m.serveMMIO(boot.MMIOBaseAddr+10*boot.MMIODeviceSize, data, false) {
		t.Fatal("serveMMIO returned true for an address outside every registered window")
	}
}

func TestServeMMIOQueueNotify(t *testing.T) {
	m := &Machine{}
	var notified uint32
	var notifiedCount int
	base, _, _ := m.registerMMIODevice(virtio.NewTransport(1, 0, 1, 1, nil), func(queueIdx uint32) {
		notified = queueIdx
		notifiedCount++
	})

	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, 7)
	if !m.serveMMIO(base+virtio.QueueNotifyOffset, data, true) {
		t.Fatal("serveMMIO(QueueNotifyOffset) returned false, want true (handled)")
	}
	if notifiedCount != 1 || notified != 7 {
		t.Errorf("onNotify called %d times with queueIdx=%d, want 1 time with queueIdx=7", notifiedCount, notified)
	}
}
