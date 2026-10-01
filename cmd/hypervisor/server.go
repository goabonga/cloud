// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/goabonga/infrastructure/internal/hypervisor"
	"github.com/goabonga/infrastructure/internal/hypervisor/protocol"
)

// server holds the one VM this process ever boots and answers the control
// protocol for it across however many client connections come and go — a
// client reconnecting after its own restart gets a working status call
// immediately, without this process needing to remember anything about
// the previous connection.
type server struct {
	logger   *slog.Logger
	sockPath string

	mu        sync.Mutex
	machine   *hypervisor.Machine
	phase     string
	lastErr   string
	runCancel context.CancelFunc
}

func newServer(logger *slog.Logger) *server {
	return &server{logger: logger, phase: protocol.PhaseStopped}
}

// listenAndServe creates sockPath (removing a stale leftover file first,
// the same accommodation internal/manager's startVMM makes before
// spawning this process) and serves connections until one of them
// sends a shutdown request, which this method does not return from — the
// process exits from inside handleShutdown instead, since by that point
// there is nothing left to serve.
func (s *server) listenAndServe(sockPath string) error {
	s.sockPath = sockPath
	if err := os.Remove(sockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("hypervisor: remove stale socket %q: %w", sockPath, err)
	}
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return fmt.Errorf("hypervisor: listen on %q: %w", sockPath, err)
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			return fmt.Errorf("hypervisor: accept: %w", err)
		}
		s.serveConn(conn)
	}
}

// serveConn handles every request on one connection until the client
// disconnects (a normal event: the VM keeps running, and listenAndServe's
// loop accepts the next connection) or a request is malformed enough that
// the connection can no longer be trusted to frame further requests
// correctly.
func (s *server) serveConn(conn net.Conn) {
	defer conn.Close()
	dec := protocol.NewDecoder(conn)
	enc := protocol.NewEncoder(conn)

	for {
		var req protocol.Request
		if err := dec.Decode(&req); err != nil {
			if !errors.Is(err, io.EOF) {
				s.logger.Warn("control connection closing on a decode error", "err", err)
			}
			return
		}

		result, err := s.dispatch(req)
		resp := protocol.Response{ID: req.ID, OK: err == nil}
		if err != nil {
			resp.Error = err.Error()
		} else {
			resp.Result = result
		}
		if err := enc.Encode(resp); err != nil {
			s.logger.Warn("control connection closing on an encode error", "err", err)
			return
		}
	}
}

func (s *server) dispatch(req protocol.Request) (json.RawMessage, error) {
	switch req.Type {
	case protocol.TypeCreate:
		var params protocol.CreateParams
		if err := json.Unmarshal(req.Payload, &params); err != nil {
			return nil, fmt.Errorf("decode create payload: %w", err)
		}
		return s.handleCreate(params)
	case protocol.TypeStatus:
		return s.handleStatus()
	case protocol.TypeShutdown:
		return nil, s.handleShutdown()
	default:
		return nil, fmt.Errorf("unknown request type %q", req.Type)
	}
}

// handleCreate boots the VM once; a second call (e.g. a reconnecting
// client that doesn't know whether its first create landed) is answered
// from the already-running machine instead of trying to boot a second one,
// matching EnsureMicroVM's documented idempotency contract one layer up.
func (s *server) handleCreate(params protocol.CreateParams) (json.RawMessage, error) {
	if err := validateCreateParams(params); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.machine != nil {
		return s.statusLocked(), nil
	}

	m, err := hypervisor.New(hypervisor.Config{
		VCPUs:        params.VCPUs,
		MemoryMB:     params.MemoryMB,
		KernelPath:   params.KernelPath,
		InitrdPath:   params.InitrdPath,
		CmdLine:      params.CmdLine,
		Console:      os.Stdout,
		TapName:      params.TapName,
		MAC:          params.MAC,
		DiskPath:     params.DiskPath,
		DiskReadonly: params.DiskReadonly,
	})
	if err != nil {
		s.phase = protocol.PhaseError
		s.lastErr = err.Error()
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.machine = m
	s.runCancel = cancel
	s.phase = protocol.PhaseRunning
	s.logger.Info("vm created", "vcpus", params.VCPUs, "memoryMB", params.MemoryMB, "kernel", params.KernelPath)

	go s.run(ctx, m)

	return s.statusLocked(), nil
}

// run drives the vcpu run loop for the lifetime of the VM, updating phase
// once it stops for any reason — including a clean shutdown, which cancels
// ctx itself (see handleShutdown) before this returns.
func (s *server) run(ctx context.Context, m *hypervisor.Machine) {
	err := m.Run(ctx, nil)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.phase = protocol.PhaseError
		s.lastErr = err.Error()
		s.logger.Error("vm run loop stopped", "err", err)
	} else if s.phase != protocol.PhaseStopped {
		// Not already PhaseStopped means this wasn't a shutdown request
		// (which sets it before cancelling ctx) — ctx was cancelled for
		// some other reason, which shouldn't happen today but isn't
		// this goroutine's call to treat as an error either.
		s.phase = protocol.PhaseStopped
	}
}

func (s *server) handleStatus() (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked(), nil
}

// statusLocked must be called with s.mu held.
func (s *server) statusLocked() json.RawMessage {
	result := protocol.StatusResult{
		Phase: s.phase,
		PID:   os.Getpid(),
		Error: s.lastErr,
	}
	b, _ := json.Marshal(result) // a fixed, always-marshalable struct
	return b
}

// handleShutdown stops the vcpu run loop, tears the VM down, and exits the
// process — the manager's existing grace-period-then-SIGKILL pattern
// (chvShutdownGrace in internal/manager/microvm.go) handles the rest at
// the process level and needs no cooperation from this method beyond
// exiting promptly.
func (s *server) handleShutdown() error {
	s.teardown()
	s.logger.Info("vm shutdown")
	go func() {
		time.Sleep(50 * time.Millisecond) // let the response flush before the process exits
		os.Exit(0)
	}()
	return nil
}

// HandleSignal is the SIGTERM/SIGINT path (main.go): unlike
// handleShutdown, there is no client connection to flush a response to
// first, so it tears down and exits immediately. Both paths funnel
// through the same teardown, so a VM stopped by a signal gets the same
// clean fd/resource hygiene as one stopped by the shutdown request.
func (s *server) HandleSignal(sig os.Signal) {
	s.logger.Info("received signal, shutting down", "signal", sig)
	s.teardown()
	os.Exit(0)
}

// teardown stops the vcpu run loop, closes the Machine (every KVM, tap
// and disk fd, guest memory), and removes the control socket file — every
// resource this process holds, in the order that lets the run loop
// notice it's being cancelled before its fds are pulled out from under
// it. Safe to call more than once (Machine.Close already is) and from
// either handleShutdown or HandleSignal.
func (s *server) teardown() {
	s.mu.Lock()
	m := s.machine
	cancel := s.runCancel
	s.phase = protocol.PhaseStopped
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if m != nil {
		// Give the run loop a moment to notice ctx is cancelled and
		// return before tearing down the fds it's using; Close itself
		// is safe to call concurrently with a still-running loop
		// (it would just make the next KVM_RUN fail), but this avoids
		// that race in the common case.
		time.Sleep(50 * time.Millisecond)
		_ = m.Close()
	}
	if s.sockPath != "" {
		_ = os.Remove(s.sockPath)
	}
}
