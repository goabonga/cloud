// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// chBinary is the default cloud-hypervisor executable name, resolved through
// PATH like iptables and cryptsetup are elsewhere in this package.
const chBinary = "cloud-hypervisor"

// chStartupTimeout bounds how long EnsureMicroVM waits for the freshly
// started cloud-hypervisor process to open its API socket.
const chStartupTimeout = 5 * time.Second

// chCPUsConfig is the "cpus" section of a cloud-hypervisor VmConfig.
type chCPUsConfig struct {
	BootVCPUs int `json:"boot_vcpus"`
	MaxVCPUs  int `json:"max_vcpus"`
}

// chMemoryConfig is the "memory" section of a cloud-hypervisor VmConfig.
type chMemoryConfig struct {
	SizeBytes int64 `json:"size"`
}

// chPayloadConfig is the "payload" section: kernel, optional initramfs and
// the kernel command line.
type chPayloadConfig struct {
	Kernel    string `json:"kernel"`
	Initramfs string `json:"initramfs,omitempty"`
	Cmdline   string `json:"cmdline,omitempty"`
}

// chDiskConfig describes one block device attached to the VM.
type chDiskConfig struct {
	Path     string `json:"path"`
	Readonly bool   `json:"readonly,omitempty"`
}

// chNetConfig attaches an existing, already-configured TAP device by name;
// cloud-hypervisor opens it itself, so the agent never touches the tap fd.
type chNetConfig struct {
	Tap string `json:"tap"`
}

// chConsoleConfig is shared by the "serial" and "console" VmConfig sections.
type chConsoleConfig struct {
	Mode string `json:"mode"`
}

// chVMConfig is the subset of cloud-hypervisor's VmConfig this agent drives:
// https://github.com/cloud-hypervisor/cloud-hypervisor's vm.create payload.
type chVMConfig struct {
	CPUs    chCPUsConfig    `json:"cpus"`
	Memory  chMemoryConfig  `json:"memory"`
	Payload chPayloadConfig `json:"payload"`
	Disks   []chDiskConfig  `json:"disks,omitempty"`
	Net     []chNetConfig   `json:"net,omitempty"`
	Serial  chConsoleConfig `json:"serial"`
	Console chConsoleConfig `json:"console"`
}

// vmmHandle is a running cloud-hypervisor process: its pid and the unix
// socket its REST API listens on.
type vmmHandle struct {
	pid  int
	sock string
}

// startVMM launches a cloud-hypervisor process with its API socket at sock,
// detached so it survives the agent, and waits for the socket to appear.
func startVMM(ctx context.Context, binary, sock, logPath string) (*vmmHandle, error) {
	if binary == "" {
		binary = chBinary
	}
	_ = os.Remove(sock)

	// #nosec G204 -- binary is agent-configured (defaults to "cloud-hypervisor"
	// resolved through PATH), not user input.
	cmd := exec.Command(binary, "--api-socket", sock)
	logf, err := os.Create(logPath) // #nosec G304 -- agent-owned log path
	if err == nil {
		cmd.Stdout = logf
		cmd.Stderr = logf
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("manager: start %s: %w", binary, err)
	}
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(chStartupTimeout)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("manager: %s did not open %s within %s", binary, sock, chStartupTimeout)
		}
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return &vmmHandle{pid: cmd.Process.Pid, sock: sock}, nil
}

// processAlive reports whether pid is a live process, the same liveness test
// compute's cgroup teardown relies on implicitly via `kill -9`.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// vmmClient drives one cloud-hypervisor instance's REST API over its unix
// socket with the stdlib HTTP client, no third-party SDK.
type vmmClient struct {
	http *http.Client
}

func newVMMClient(sock string) *vmmClient {
	return &vmmClient{http: &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		},
		Timeout: 10 * time.Second,
	}}
}

// put issues a PUT to the cloud-hypervisor API with an optional JSON body.
func (c *vmmClient) put(ctx context.Context, path string, body any) error {
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("manager: encode %s: %w", path, err)
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://cloud-hypervisor.sock"+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("manager: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("manager: %s: status %d: %s", path, resp.StatusCode, string(data))
	}
	return nil
}

// createVM sends vm.create, which configures but does not start the VM.
func (c *vmmClient) createVM(ctx context.Context, cfg chVMConfig) error {
	return c.put(ctx, "/api/v1/vm.create", cfg)
}

// bootVM sends vm.boot, which starts a previously created VM.
func (c *vmmClient) bootVM(ctx context.Context) error {
	return c.put(ctx, "/api/v1/vm.boot", nil)
}

// shutdownVMM asks the whole cloud-hypervisor process to exit, which powers
// the VM off first. Best effort: a process that doesn't respond is killed by
// the caller.
func (c *vmmClient) shutdownVMM(ctx context.Context) error {
	return c.put(ctx, "/api/v1/vmm.shutdown", nil)
}
