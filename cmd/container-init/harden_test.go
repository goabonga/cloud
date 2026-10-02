// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package main

import (
	"os"
	"os/exec"
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

func TestHardenInChild(t *testing.T) {
	if os.Getenv("INFRA_INIT_TEST_CHILD") == "1" {
		runtime.LockOSThread()
		if err := harden(); err != nil {
			t.Fatal(err)
		}
		nnp, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
		if err != nil || nnp != 1 {
			t.Fatalf("no_new_privs %d %v", nnp, err)
		}
		header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
		data := [2]unix.CapUserData{}
		if err := unix.Capget(&header, &data[0]); err != nil {
			t.Fatal(err)
		}
		if data[0].Effective != 0 || data[1].Effective != 0 || data[0].Permitted != 0 || data[1].Permitted != 0 {
			t.Fatal("capabilities retained")
		}
		if err := unix.Unshare(unix.CLONE_NEWUSER); err != unix.EPERM {
			t.Fatalf("unshare: %v", err)
		}
		if err := unix.Exec("/bin/true", []string{"true"}, os.Environ()); err != nil {
			t.Fatal(err)
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHardenInChild$")
	cmd.Env = append(os.Environ(), "INFRA_INIT_TEST_CHILD=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child: %v %s", err, out)
	}
}

func TestInstallSeccompRejectsInvalidLengths(t *testing.T) {
	for _, length := range []int{0, 4097, 65536} {
		if err := installSeccomp(make([]unix.SockFilter, length)); err == nil {
			t.Fatalf("accepted invalid filter length %d", length)
		}
	}
}
