// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGuestCmdline(t *testing.T) {
	cases := []struct {
		name string
		req  MicroVMRequest
		want string
	}{
		{
			name: "no network info and no extra cmdline",
			req:  MicroVMRequest{},
			want: "",
		},
		{
			name: "extra cmdline only",
			req:  MicroVMRequest{CmdLine: "console=ttyS0"},
			want: "console=ttyS0",
		},
		{
			name: "static ip and cloud-init seed appended after the extra cmdline",
			req:  MicroVMRequest{UID: "vm-1", CmdLine: "console=ttyS0", IP: "10.0.1.10", Gateway: "10.0.1.1", Prefix: 24},
			want: "console=ttyS0 net.ifnames=0 biosdevname=0 ip=10.0.1.10::10.0.1.1:255.255.255.0::eth0:off ds=nocloud-net;s=http://10.0.1.1:8912/vm-1/",
		},
		{
			name: "static ip and cloud-init seed with no extra cmdline",
			req:  MicroVMRequest{UID: "vm-1", IP: "10.0.1.10", Gateway: "10.0.1.1", Prefix: 24},
			want: "net.ifnames=0 biosdevname=0 ip=10.0.1.10::10.0.1.1:255.255.255.0::eth0:off ds=nocloud-net;s=http://10.0.1.1:8912/vm-1/",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := guestCmdline(tc.req); got != tc.want {
				t.Fatalf("guestCmdline() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadAlivePidAndWritePidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chv.pid")

	if _, ok := readAlivePid(path); ok {
		t.Fatal("expected no pid for a missing file")
	}

	if err := writePidFile(path, os.Getpid()); err != nil {
		t.Fatalf("write pid file: %v", err)
	}
	pid, ok := readAlivePid(path)
	if !ok || pid != os.Getpid() {
		t.Fatalf("readAlivePid() = (%d, %v), want (%d, true)", pid, ok, os.Getpid())
	}

	if err := os.WriteFile(path, []byte("999999999"), 0o644); err != nil {
		t.Fatalf("write stale pid: %v", err)
	}
	if _, ok := readAlivePid(path); ok {
		t.Fatal("expected a pid that does not exist to be reported as not alive")
	}
}
