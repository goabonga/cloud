// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/hypervisor/kvm"
	"github.com/goabonga/infrastructure/internal/hypervisor/protocol"
)

// TestHypervisorBoot is the end-to-end proof for this milestone: build the
// real cmd/hypervisor binary, spawn it as its own process the way a future
// microvm backend would, drive it entirely over its real control socket
// (create, poll status, shutdown), and confirm it exits cleanly — the
// direct analogue of TestExecMicroVMBackendBoot in microvm_test.go, for
// this hypervisor instead of cloud-hypervisor.
//
// It needs root (or kvm-group membership), a real kernel
// (GOA_ITEST_HYPERVISOR_KERNEL), and a working `go build` toolchain, or
// this test skips.
func TestHypervisorBoot(t *testing.T) {
	if _, err := os.Stat(kvm.DevicePath); err != nil {
		t.Skipf("%s not available: %v", kvm.DevicePath, err)
	}
	kernel := os.Getenv("GOA_ITEST_HYPERVISOR_KERNEL")
	if kernel == "" {
		t.Skip("GOA_ITEST_HYPERVISOR_KERNEL not set")
	}

	dir := t.TempDir()
	binPath := filepath.Join(dir, "hypervisor")
	build := exec.Command("go", "build", "-o", binPath, "github.com/goabonga/infrastructure/cmd/hypervisor")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build cmd/hypervisor: %v: %s", err, out)
	}

	sockPath := filepath.Join(dir, "control.sock")
	var console bytes.Buffer
	cmd := exec.Command(binPath, "-control-socket", sockPath)
	cmd.Stdout = &console
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start hypervisor: %v", err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil { // still running: the test failed before shutdown
			_ = cmd.Process.Kill()
		}
	})

	waitForSocket(t, sockPath, 5*time.Second)

	conn := dialControlSocket(t, sockPath)
	defer conn.Close()
	enc := protocol.NewEncoder(conn)
	dec := protocol.NewDecoder(conn)

	createParams, err := json.Marshal(protocol.CreateParams{
		VCPUs:      1,
		MemoryMB:   256,
		KernelPath: kernel,
		CmdLine:    "console=ttyS0 panic=-1",
	})
	if err != nil {
		t.Fatalf("marshal CreateParams: %v", err)
	}
	if err := enc.Encode(protocol.Request{ID: 1, Type: protocol.TypeCreate, Payload: createParams}); err != nil {
		t.Fatalf("send create: %v", err)
	}
	var resp protocol.Response
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("create failed: %s", resp.Error)
	}

	// Let the vcpu actually run for a while before asking it to stop;
	// console output accumulates in the background regardless of
	// whether anyone is reading status in the meantime.
	time.Sleep(10 * time.Second)

	if err := enc.Encode(protocol.Request{ID: 2, Type: protocol.TypeStatus}); err != nil {
		t.Fatalf("send status: %v", err)
	}
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("status failed: %s", resp.Error)
	}
	var status protocol.StatusResult
	if err := json.Unmarshal(resp.Result, &status); err != nil {
		t.Fatalf("unmarshal status result: %v", err)
	}
	if status.PID != cmd.Process.Pid {
		t.Errorf("status.PID = %d, want the hypervisor process's own pid %d", status.PID, cmd.Process.Pid)
	}

	if err := enc.Encode(protocol.Request{ID: 3, Type: protocol.TypeShutdown}); err != nil {
		t.Fatalf("send shutdown: %v", err)
	}
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("decode shutdown response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("shutdown failed: %s", resp.Error)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case err := <-waitErr:
		if err != nil {
			t.Fatalf("hypervisor process exited with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hypervisor process did not exit within 5s of a shutdown request")
	}

	if !strings.Contains(console.String(), "Linux version") {
		t.Fatalf("console output does not contain the kernel boot banner; got %d bytes:\n%s", console.Len(), console.String())
	}
}
