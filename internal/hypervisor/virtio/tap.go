// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package virtio

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// OpenTap re-opens an already-persistent TAP device — one
// internal/manager's ExecBackend already created and attached to the VPC
// bridge the same way it did for cloud-hypervisor before (`ip tuntap add
// ... mode tap`) — and returns its raw character-device fd configured for
// plain Ethernet frame I/O: IFF_TAP (link-layer frames, not IFF_TUN's
// IP-layer packets) and IFF_NO_PI (no 4-byte packet-info header
// cloud-hypervisor's by-name TAP attachment never has to think about,
// since it never touches the fd itself — this package does, so it must).
// No VIRTIO_NET_F_* offload feature is negotiated, so there is no
// virtio_net_hdr handling needed at this layer either; net.go prepends/
// strips that header itself.
//
// The returned file is non-blocking and was opened via a raw syscall
// rather than os.OpenFile specifically so it can be wrapped with
// os.NewFile while already non-blocking: per os.NewFile's documented
// behavior, that makes the result "pollable" (its Read integrates with
// the runtime's netpoller), which is what lets a goroutine blocked on
// Read be safely unblocked by another goroutine's Close on shutdown — a
// bare unix.Read in a loop would not be.
func OpenTap(name string) (*os.File, error) {
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("virtio: open /dev/net/tun: %w", err)
	}

	ifr, err := tapIfreq(name)
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	if err := unix.IoctlIfreq(fd, unix.TUNSETIFF, ifr); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("virtio: TUNSETIFF %q: %w", name, err)
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("virtio: set %q non-blocking: %w", name, err)
	}

	return os.NewFile(uintptr(fd), name), nil
}

// tapIfreq builds the ifreq TUNSETIFF expects: name, plus IFF_TAP|IFF_NO_PI
// in its flags union field. Split out from OpenTap so the part that
// doesn't need CAP_NET_ADMIN or an open tun fd — name validation, flag
// construction — is unit-testable on its own.
func tapIfreq(name string) (*unix.Ifreq, error) {
	ifr, err := unix.NewIfreq(name)
	if err != nil {
		return nil, fmt.Errorf("virtio: tap device name %q: %w", name, err)
	}
	ifr.SetUint16(unix.IFF_TAP | unix.IFF_NO_PI)
	return ifr, nil
}
