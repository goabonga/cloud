// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package hypervisor

import (
	"fmt"
	"net"

	"github.com/goabonga/infrastructure/internal/hypervisor/boot"
	"github.com/goabonga/infrastructure/internal/hypervisor/virtio"
)

// defaultMAC is used when Config.TapName is set but Config.MAC isn't: a
// fixed, locally-administered address (the 0x02 low bit of the first
// octet), deterministic rather than randomly generated since nothing
// about this device's identity needs to vary between runs yet.
const defaultMAC = "02:00:00:00:00:01"

// setupNet opens cfg.TapName, creates the virtio-net device backed by it,
// and registers its MMIO window — everything Run needs to actually serve
// the device, done once during New. It returns the virtio_mmio.device=
// kernel command-line parameter the guest needs appended to find it (the
// Linux documentation's <size>@<baseaddr>:<irq> format), since the device
// must exist, with its address and GSI assigned, before the command line
// guest memory is loaded with is built.
func (m *Machine) setupNet(cfg Config) (cmdlineParam string, err error) {
	macStr := cfg.MAC
	if macStr == "" {
		macStr = defaultMAC
	}
	mac, err := parseMAC(macStr)
	if err != nil {
		return "", fmt.Errorf("mac %q: %w", macStr, err)
	}

	m.tap, err = virtio.OpenTap(cfg.TapName)
	if err != nil {
		return "", err
	}

	var gsi uint32 // filled in below; the onInterrupt closure captures it by reference, called only later
	m.net = virtio.NewNet(mac, m.tap, m, func() { m.pulseIRQ(gsi) })

	base, assignedGSI, err := m.registerMMIODevice(m.net.Transport(), m.net.HandleNotify)
	if err != nil {
		return "", err
	}
	gsi = assignedGSI

	return fmt.Sprintf(" virtio_mmio.device=%d@0x%x:%d", boot.MMIODeviceSize, base, gsi), nil
}

// parseMAC validates s as a 6-byte (EUI-48) MAC address — net.ParseMAC
// alone also accepts the longer EUI-64 (InfiniBand) form, which a virtio
// device's 6-byte config field has no room for.
func parseMAC(s string) ([6]byte, error) {
	hw, err := net.ParseMAC(s)
	if err != nil {
		return [6]byte{}, err
	}
	if len(hw) != 6 {
		return [6]byte{}, fmt.Errorf("%q is a %d-byte hardware address, want 6 (EUI-48)", s, len(hw))
	}
	var mac [6]byte
	copy(mac[:], hw)
	return mac, nil
}
