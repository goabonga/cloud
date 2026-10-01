// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package kvm

import "golang.org/x/sys/unix"

// Linux encodes an ioctl request number from a direction, a one-byte "type"
// (here always kvmIOCType), an 8-bit command number and the size of the
// argument it carries, per include/uapi/asm-generic/ioctl.h. x/sys/unix
// does not expose these for arbitrary ioctls (only for its own typed
// helpers), so KVM's request numbers are derived by hand here and checked
// against /usr/include/linux/kvm.h by the tests in kvm_test.go.
const (
	iocNone  = 0
	iocWrite = 1
	iocRead  = 2

	iocNRBits   = 8
	iocTypeBits = 8
	iocSizeBits = 14

	iocNRShift   = 0
	iocTypeShift = iocNRShift + iocNRBits
	iocSizeShift = iocTypeShift + iocTypeBits
	iocDirShift  = iocSizeShift + iocSizeBits

	// kvmIOCType is KVMIO, the ioctl "type" byte every KVM request shares.
	kvmIOCType = 0xAE
)

func ioc(dir, nr, size uintptr) uintptr {
	return dir<<iocDirShift | kvmIOCType<<iocTypeShift | nr<<iocNRShift | size<<iocSizeShift
}

// io encodes a no-argument KVM ioctl request.
func io(nr uintptr) uintptr { return ioc(iocNone, nr, 0) }

// iow encodes a KVM ioctl request that carries an argument of the given
// size from userspace to the kernel.
func iow(nr, size uintptr) uintptr { return ioc(iocWrite, nr, size) }

// ior encodes a KVM ioctl request that carries an argument of the given
// size back from the kernel to userspace.
func ior(nr, size uintptr) uintptr { return ioc(iocRead, nr, size) }

// ioctl issues a raw ioctl(2) and returns its return value, which several
// KVM requests (KVM_CREATE_VM, KVM_CREATE_VCPU, KVM_GET_API_VERSION, ...)
// use to carry a result instead of (or in addition to) an out-parameter.
func ioctl(fd int, req uintptr, arg uintptr) (int, error) {
	r1, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, arg)
	if errno != 0 {
		return 0, errno
	}
	return int(r1), nil
}
