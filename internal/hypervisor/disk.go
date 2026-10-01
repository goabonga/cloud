// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package hypervisor

import (
	"fmt"
	"os"

	"github.com/goabonga/infrastructure/internal/hypervisor/boot"
	"github.com/goabonga/infrastructure/internal/hypervisor/virtio"
)

// setupDisk opens cfg.DiskPath, creates the virtio-blk device backed by
// it, and registers its MMIO window — the block-device analogue of
// setupNet, called the same way and for the same reason: the device's
// address and GSI must be assigned before the kernel command line
// referencing it is built. The disk file must already exist; this
// package never fetches, creates or clones one (that stays
// internal/manager's vmImageCache's job, the same role it had for
// cloud-hypervisor before).
func (m *Machine) setupDisk(cfg Config) (cmdlineParam string, err error) {
	flag := os.O_RDWR
	if cfg.DiskReadonly {
		flag = os.O_RDONLY
	}
	m.disk, err = os.OpenFile(cfg.DiskPath, flag, 0)
	if err != nil {
		return "", fmt.Errorf("open disk: %w", err)
	}
	info, err := m.disk.Stat()
	if err != nil {
		return "", fmt.Errorf("stat disk: %w", err)
	}

	var gsi uint32 // filled in below; the onInterrupt closure captures it by reference, called only later — see setupNet's identical pattern
	blk := virtio.NewBlk(m.disk, info.Size(), cfg.DiskReadonly, m, func() { m.pulseIRQ(gsi) })

	base, assignedGSI, err := m.registerMMIODevice(blk.Transport(), blk.HandleNotify)
	if err != nil {
		return "", err
	}
	gsi = assignedGSI

	return fmt.Sprintf(" virtio_mmio.device=%d@0x%x:%d", boot.MMIODeviceSize, base, gsi), nil
}
