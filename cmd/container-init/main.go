// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Command infra-container-init drops workload privileges before executing its command.
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/goabonga/infrastructure/internal/meta"
)

func main() {
	if len(os.Args) == 1 {
		run(os.Stdout)
		return
	}
	args := os.Args[1:]
	if args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		log.Fatal("container-init: missing command")
	}
	binary, err := exec.LookPath(args[0])
	if err != nil {
		log.Fatal(err)
	}
	runtime.LockOSThread()
	if err := harden(); err != nil {
		log.Fatal(err)
	}
	if err := unix.Exec(binary, args, os.Environ()); err != nil {
		log.Fatal(err)
	}
}

func run(stdout io.Writer) {
	_, _ = fmt.Fprintln(stdout, meta.Line("infra-container-init", Version))
}

// harden prevents gaining privileges at exec and blocks host administration.
func harden() error {
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	data := [2]unix.CapUserData{}
	if err := unix.Capset(&header, &data[0]); err != nil {
		return err
	}
	var arch uint32
	switch runtime.GOARCH {
	case "amd64":
		arch = unix.AUDIT_ARCH_X86_64
	case "arm64":
		arch = unix.AUDIT_ARCH_AARCH64
	default:
		return fmt.Errorf("unsupported seccomp architecture %s", runtime.GOARCH)
	}
	filter := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 4},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: arch, Jt: 1},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_KILL_PROCESS},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
	}
	if runtime.GOARCH == "amd64" {
		filter = append(filter, unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JGE | unix.BPF_K, K: 0x40000000, Jf: 1}, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_KILL_PROCESS})
	}
	for _, call := range []uint32{unix.SYS_MOUNT, unix.SYS_UMOUNT2, unix.SYS_SETNS, unix.SYS_UNSHARE, unix.SYS_PTRACE, unix.SYS_BPF, unix.SYS_INIT_MODULE, unix.SYS_FINIT_MODULE, unix.SYS_DELETE_MODULE, unix.SYS_REBOOT, unix.SYS_KEXEC_LOAD} {
		filter = append(filter, unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: call, Jf: 1}, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)})
	}
	filter = append(filter, unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW})
	return installSeccomp(filter)
}

// installSeccomp validates the kernel BPF instruction limit before narrowing its length.
func installSeccomp(filter []unix.SockFilter) error {
	length := len(filter)
	if length < 1 || length > 4096 {
		return fmt.Errorf("seccomp filter length %d is outside [1, 4096]", length)
	}
	program := unix.SockFprog{Len: uint16(length), Filter: &filter[0]}
	// The prctl ABI requires a pointer to this validated kernel structure. The
	// synchronous syscall keeps program live; KeepAlive retains its backing filter.
	// #nosec G103 -- audited Linux ABI pointer; no pointer arithmetic or retained kernel pointer.
	_, _, errno := unix.Syscall(unix.SYS_PRCTL, unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&program)))
	runtime.KeepAlive(filter)
	if errno != 0 {
		return errno
	}
	return nil
}
