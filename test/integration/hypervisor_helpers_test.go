// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

//go:build integration

package integration

import (
	"net"
	"os"
	"testing"
	"time"
)

// waitForSocket polls for path to appear, the same accommodation
// startVMM makes for cloud-hypervisor's own socket (internal/manager/vmm.go)
// — cmd/hypervisor creates its control socket itself shortly after
// starting, not atomically with process creation.
func waitForSocket(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("control socket %q did not appear within %s", path, timeout)
}

func dialControlSocket(t *testing.T, path string) net.Conn {
	t.Helper()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial control socket %q: %v", path, err)
	}
	return conn
}
