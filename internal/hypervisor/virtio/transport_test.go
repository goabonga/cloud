// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package virtio_test

import (
	"encoding/binary"
	"testing"

	"github.com/goabonga/infrastructure/internal/hypervisor/virtio"
)

func readReg(t *testing.T, tr *virtio.Transport, offset uint64) uint32 {
	t.Helper()
	data := make([]byte, 4)
	tr.Read(offset, data)
	return binary.LittleEndian.Uint32(data)
}

func writeReg(tr *virtio.Transport, offset uint64, v uint32) {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, v)
	tr.Write(offset, data)
}

func TestTransportIdentity(t *testing.T) {
	tr := virtio.NewTransport(1, 0, 1, 256, nil)
	if got := readReg(t, tr, 0x000); got != 0x74726976 {
		t.Errorf("MagicValue = 0x%08x, want 0x74726976 (\"virt\")", got)
	}
	if got := readReg(t, tr, 0x004); got != 2 {
		t.Errorf("Version = %d, want 2 (non-legacy)", got)
	}
	if got := readReg(t, tr, 0x008); got != 1 {
		t.Errorf("DeviceID = %d, want 1", got)
	}
}

func TestTransportFeaturesPaging(t *testing.T) {
	// A feature bit in the high 32 bits (bit 33) plus VIRTIO_F_VERSION_1
	// (bit 32, added automatically by NewTransport) exercises both pages.
	const deviceFeatureBit33 = 1 << 33
	tr := virtio.NewTransport(1, deviceFeatureBit33, 1, 256, nil)

	writeReg(tr, 0x014, 0) // DeviceFeaturesSel = 0 (low 32 bits)
	if got := readReg(t, tr, 0x010); got != 0 {
		t.Errorf("DeviceFeatures page 0 = 0x%x, want 0 (no low-page bits offered)", got)
	}
	writeReg(tr, 0x014, 1)                     // DeviceFeaturesSel = 1 (high 32 bits)
	want := uint32(deviceFeatureBit33>>32) | 1 // bit 33 and bit 32 (VIRTIO_F_VERSION_1) both land in page 1
	if got := readReg(t, tr, 0x010); got != want {
		t.Errorf("DeviceFeatures page 1 = 0x%x, want 0x%x", got, want)
	}
}

func TestTransportDriverFeaturesRoundTrip(t *testing.T) {
	tr := virtio.NewTransport(1, 0, 1, 256, nil)

	writeReg(tr, 0x024, 0) // DriverFeaturesSel = 0
	writeReg(tr, 0x020, 0xdeadbeef)
	writeReg(tr, 0x024, 1) // DriverFeaturesSel = 1
	writeReg(tr, 0x020, 0xcafef00d)

	want := uint64(0xcafef00d)<<32 | 0xdeadbeef
	if got := tr.DriverFeatures(); got != want {
		t.Errorf("DriverFeatures() = 0x%x, want 0x%x", got, want)
	}
}

func TestTransportStatusStateMachine(t *testing.T) {
	tr := virtio.NewTransport(1, 0, 1, 256, nil)
	if tr.IsDriverOK() {
		t.Fatal("IsDriverOK() = true before any status write")
	}

	writeReg(tr, 0x070, virtio.StatusAcknowledge)
	writeReg(tr, 0x070, virtio.StatusAcknowledge|virtio.StatusDriver)
	writeReg(tr, 0x070, virtio.StatusAcknowledge|virtio.StatusDriver|virtio.StatusFeaturesOK)
	if got := readReg(t, tr, 0x070); got != virtio.StatusAcknowledge|virtio.StatusDriver|virtio.StatusFeaturesOK {
		t.Fatalf("Status readback = 0x%x after FEATURES_OK, want it set", got)
	}
	writeReg(tr, 0x070, virtio.StatusAcknowledge|virtio.StatusDriver|virtio.StatusFeaturesOK|virtio.StatusDriverOK)
	if !tr.IsDriverOK() {
		t.Fatal("IsDriverOK() = false after writing DRIVER_OK")
	}
}

func TestTransportStatusZeroResets(t *testing.T) {
	tr := virtio.NewTransport(1, 0, 1, 256, nil)
	writeReg(tr, 0x070, virtio.StatusAcknowledge|virtio.StatusDriver|virtio.StatusDriverOK)
	writeReg(tr, 0x024, 1)
	writeReg(tr, 0x020, 0xffffffff)
	writeReg(tr, 0x030, 0)   // QueueSel = 0
	writeReg(tr, 0x038, 128) // QueueNum
	writeReg(tr, 0x044, 1)   // QueueReady

	writeReg(tr, 0x070, 0) // Status = 0: reset

	if tr.IsDriverOK() {
		t.Error("IsDriverOK() = true after a status reset")
	}
	if got := tr.DriverFeatures(); got != 0 {
		t.Errorf("DriverFeatures() = 0x%x after reset, want 0", got)
	}
	if q := tr.Queue(0); q.Ready || q.Size != 0 {
		t.Errorf("Queue(0) = %+v after reset, want zero value", q)
	}
}

func TestTransportQueueConfiguration(t *testing.T) {
	tr := virtio.NewTransport(2, 0, 2, 256, nil)

	if got := readReg(t, tr, 0x034); got != 256 { // QueueNumMax
		t.Errorf("QueueNumMax = %d, want 256", got)
	}

	writeReg(tr, 0x030, 1)      // QueueSel = 1
	writeReg(tr, 0x038, 64)     // QueueNum
	writeReg(tr, 0x080, 0x1000) // QueueDescLow
	writeReg(tr, 0x084, 0x0000) // QueueDescHigh
	writeReg(tr, 0x090, 0x2000) // QueueDriverLow (avail)
	writeReg(tr, 0x094, 0x0000)
	writeReg(tr, 0x0a0, 0x3000) // QueueDeviceLow (used)
	writeReg(tr, 0x0a4, 0x0000)
	writeReg(tr, 0x044, 1) // QueueReady = 1

	q := tr.Queue(1)
	if q.Size != 64 || !q.Ready || q.Desc != 0x1000 || q.Avail != 0x2000 || q.Used != 0x3000 {
		t.Errorf("Queue(1) = %+v, want {Size:64 Ready:true Desc:0x1000 Avail:0x2000 Used:0x3000}", q)
	}
	// Queue 0 (never touched) must be unaffected by configuring queue 1.
	if q0 := tr.Queue(0); q0.Ready {
		t.Errorf("Queue(0) = %+v, want untouched", q0)
	}
}

func TestTransportQueueAddressHighWord(t *testing.T) {
	tr := virtio.NewTransport(2, 0, 1, 256, nil)
	writeReg(tr, 0x030, 0)
	writeReg(tr, 0x080, 0x00001000) // low
	writeReg(tr, 0x084, 0x00000002) // high
	if got := tr.Queue(0).Desc; got != 0x0000000200001000 {
		t.Errorf("Queue(0).Desc = 0x%x, want 0x0000000200001000", got)
	}
}

func TestTransportOutOfRangeQueueSelIsIgnoredNotPanicking(t *testing.T) {
	tr := virtio.NewTransport(1, 0, 1, 256, nil) // only queue 0 exists
	writeReg(tr, 0x030, 5)                       // QueueSel = 5, out of range
	writeReg(tr, 0x038, 64)                      // must not panic
	if got := readReg(t, tr, 0x044); got != 0 {
		t.Errorf("QueueReady read with an out-of-range selection = %d, want 0", got)
	}
}

func TestTransportInterruptStatusAndACK(t *testing.T) {
	tr := virtio.NewTransport(1, 0, 1, 256, nil)
	if got := readReg(t, tr, 0x060); got != 0 {
		t.Fatalf("InterruptStatus = 0x%x before any interrupt, want 0", got)
	}
	tr.RaiseUsedBufferInterrupt()
	if got := readReg(t, tr, 0x060); got != virtio.UsedBufferInterrupt {
		t.Fatalf("InterruptStatus = 0x%x after RaiseUsedBufferInterrupt, want 0x%x", got, virtio.UsedBufferInterrupt)
	}
	writeReg(tr, 0x064, virtio.UsedBufferInterrupt) // InterruptACK
	if got := readReg(t, tr, 0x060); got != 0 {
		t.Fatalf("InterruptStatus = 0x%x after ACK, want 0", got)
	}
}

func TestTransportConfigSpace(t *testing.T) {
	config := []byte{0xde, 0xad, 0xbe, 0xef}
	tr := virtio.NewTransport(1, 0, 1, 256, config)

	got := make([]byte, 4)
	tr.Read(virtio.ConfigSpaceOffset, got)
	if string(got) != string(config) {
		t.Errorf("config read = % x, want % x", got, config)
	}

	// Reading past the end of a short config space zero-fills rather
	// than returning garbage or panicking.
	tail := make([]byte, 2)
	tr.Read(virtio.ConfigSpaceOffset+3, tail)
	if tail[0] != 0xef || tail[1] != 0 {
		t.Errorf("config tail read = % x, want {0xef, 0x00}", tail)
	}

	// Config space is read-only; a write must not change anything.
	tr.Write(virtio.ConfigSpaceOffset, []byte{0, 0, 0, 0})
	tr.Read(virtio.ConfigSpaceOffset, got)
	if string(got) != string(config) {
		t.Errorf("config read after a write = % x, want unchanged % x", got, config)
	}
}
