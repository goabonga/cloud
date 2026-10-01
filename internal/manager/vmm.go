// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/goabonga/infrastructure/internal/hypervisor/protocol"
)

// hypervisorBinary is the default infra-hypervisor executable name,
// resolved through PATH like iptables and cryptsetup are elsewhere in
// this package. It ships inside the infra-agent .deb alongside infra-lb
// (see packaging/build-debs.sh), not installed separately the way
// cloud-hypervisor used to be.
const hypervisorBinary = "infra-hypervisor"

// hypervisorStartupTimeout bounds how long EnsureMicroVM waits for the
// freshly started infra-hypervisor process to open its control socket.
const hypervisorStartupTimeout = 5 * time.Second

// vmmHandle is a running infra-hypervisor process: its pid and the unix
// socket its control protocol (internal/hypervisor/protocol) listens on.
type vmmHandle struct {
	pid  int
	sock string
}

// startVMM launches an infra-hypervisor process with its control socket at
// sock, detached so it survives the agent, and waits for the socket to
// appear.
func startVMM(ctx context.Context, binary, sock, logPath string) (*vmmHandle, error) {
	if binary == "" {
		binary = hypervisorBinary
	}
	_ = os.Remove(sock)

	// #nosec G204 -- binary is agent-configured (defaults to
	// "infra-hypervisor" resolved through PATH), not user input.
	cmd := exec.Command(binary, "-control-socket", sock)
	logf, err := os.Create(logPath) // #nosec G304 -- agent-owned log path
	if err == nil {
		cmd.Stdout = logf // the guest's own serial console output
		cmd.Stderr = logf // infra-hypervisor's structured diagnostic log
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("manager: start %s: %w", binary, err)
	}
	go func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(hypervisorStartupTimeout)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			return nil, fmt.Errorf("manager: %s did not open %s within %s", binary, sock, hypervisorStartupTimeout)
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

// vmmClient drives one infra-hypervisor instance's control protocol
// (internal/hypervisor/protocol) over its unix socket: newline-delimited
// JSON, not REST — see docs/architecture/go-hypervisor.md for why.
type vmmClient struct {
	conn   net.Conn
	enc    *protocol.Encoder
	dec    *protocol.Decoder
	nextID int
}

func newVMMClient(ctx context.Context, sock string) (*vmmClient, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", sock)
	if err != nil {
		return nil, fmt.Errorf("manager: connect to %s: %w", sock, err)
	}
	return &vmmClient{conn: conn, enc: protocol.NewEncoder(conn), dec: protocol.NewDecoder(conn)}, nil
}

func (c *vmmClient) Close() error {
	return c.conn.Close()
}

// call sends one request and returns its response, turning a
// not-OK response into a Go error the same way the old REST client turned
// a >=300 HTTP status into one.
func (c *vmmClient) call(reqType string, payload any) (*protocol.Response, error) {
	c.nextID++
	req := protocol.Request{ID: c.nextID, Type: reqType}
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("manager: encode %s: %w", reqType, err)
		}
		req.Payload = data
	}
	if err := c.enc.Encode(req); err != nil {
		return nil, fmt.Errorf("manager: send %s: %w", reqType, err)
	}
	var resp protocol.Response
	if err := c.dec.Decode(&resp); err != nil {
		return nil, fmt.Errorf("manager: receive %s response: %w", reqType, err)
	}
	if !resp.OK {
		return nil, fmt.Errorf("manager: %s: %s", reqType, resp.Error)
	}
	return &resp, nil
}

// create boots the VM: infra-hypervisor combines what was cloud-hypervisor's
// separate vm.create and vm.boot calls into this single request, since an
// infra-hypervisor process only ever boots once.
func (c *vmmClient) create(params protocol.CreateParams) error {
	_, err := c.call(protocol.TypeCreate, params)
	return err
}

// shutdown asks the whole infra-hypervisor process to exit, which tears
// the VM and every fd it holds down first. Best effort: a process that
// doesn't respond is killed by the caller.
func (c *vmmClient) shutdown() error {
	_, err := c.call(protocol.TypeShutdown, nil)
	return err
}
