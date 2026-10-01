// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package virtio_test

import "fmt"

// fakeMemory is a plain []byte-backed GuestMemory, standing in for real
// KVM-mapped memory in every test in this package that needs one (queue,
// net, blk device tests): address 0 of the fake memory is address 0 of
// "guest" memory.
type fakeMemory []byte

func newFakeMemory(size int) fakeMemory {
	return make(fakeMemory, size)
}

func (m fakeMemory) Slice(addr uint64, length int) ([]byte, error) {
	if length < 0 || addr+uint64(length) > uint64(len(m)) {
		return nil, fmt.Errorf("fakeMemory: [0x%x, 0x%x) out of bounds (size 0x%x)", addr, addr+uint64(length), len(m))
	}
	return m[addr : addr+uint64(length)], nil
}
