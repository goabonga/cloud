// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package hypervisor

import (
	"fmt"

	"github.com/goabonga/infrastructure/internal/hypervisor/virtio"
)

var _ virtio.GuestMemory = (*Machine)(nil)

// Slice implements virtio.GuestMemory directly against the KVM-mapped
// guest memory New allocated: the returned slice aliases m.mem, so a
// device writing into it (e.g. virtio-net filling an rx buffer) is
// writing into the same memory the guest's own loads see, no copy or
// synchronization needed beyond what KVM_SET_USER_MEMORY_REGION already
// guarantees.
func (m *Machine) Slice(addr uint64, length int) ([]byte, error) {
	if length < 0 || addr+uint64(length) > uint64(len(m.mem)) { // #nosec G115 -- length < 0 is rejected by the left operand before uint64(length) is ever evaluated on a negative value
		return nil, fmt.Errorf("hypervisor: guest memory [0x%x, 0x%x) out of bounds (%d bytes of memory)", addr, addr+uint64(length), len(m.mem))
	}
	return m.mem[addr : addr+uint64(length)], nil
}
