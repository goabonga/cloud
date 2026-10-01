// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/state"
)

func TestParseArgsDefaults(t *testing.T) {
	t.Setenv("GOA_EXPORTER_ADDR", "")
	t.Setenv("GOA_STATE_DIR", "")
	t.Setenv("GOA_STATE_DSN", "")

	cfg, err := parseArgs(nil)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if cfg.addr != ":9100" {
		t.Errorf("addr = %q, want %q", cfg.addr, ":9100")
	}
	if cfg.stateDir != "./state" {
		t.Errorf("stateDir = %q, want %q", cfg.stateDir, "./state")
	}
	if cfg.stateDSN != "" {
		t.Errorf("stateDSN = %q, want empty", cfg.stateDSN)
	}
}

func TestParseArgsEnvDefaults(t *testing.T) {
	t.Setenv("GOA_EXPORTER_ADDR", ":9999")
	t.Setenv("GOA_STATE_DIR", "/var/custom-state")
	t.Setenv("GOA_STATE_DSN", "postgres://env")

	cfg, err := parseArgs(nil)
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if cfg.addr != ":9999" || cfg.stateDir != "/var/custom-state" || cfg.stateDSN != "postgres://env" {
		t.Fatalf("unexpected cfg from env: %+v", cfg)
	}
}

func TestParseArgsOverrides(t *testing.T) {
	cfg, err := parseArgs([]string{"-addr", ":1234", "-state-dir", "/tmp/foo", "-state-dsn", "postgres://x"})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if cfg.addr != ":1234" || cfg.stateDir != "/tmp/foo" || cfg.stateDSN != "postgres://x" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
}

func TestParseArgsBadFlag(t *testing.T) {
	if _, err := parseArgs([]string{"-bogus"}); err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestNewMux(t *testing.T) {
	store := state.NewFileStore(t.TempDir())
	srv := httptest.NewServer(newMux(store))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("healthz body = %q", body)
	}

	mresp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer mresp.Body.Close()
	if mresp.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d", mresp.StatusCode)
	}
	mbody, _ := io.ReadAll(mresp.Body)
	if !strings.Contains(string(mbody), "infra_resources_total") {
		t.Fatalf("metrics body missing infra_resources_total: %s", mbody)
	}
}

func TestServeGracefulShutdownReturnsErrServerClosed(t *testing.T) {
	store := state.NewFileStore(t.TempDir())
	srv := newServer(newMux(store))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- srv.Serve(ln) }()

	addr := ln.Addr().String()
	waitHealthy(t, addr)

	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d", resp.StatusCode)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() = %v", err)
	}

	select {
	case err := <-serveErrCh:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Serve() = %v, want http.ErrServerClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after Shutdown")
	}
}

func failingOpenStore(_, _ string) (state.Store, error) {
	return nil, errors.New("boom")
}

func TestRunWithOpenStoreError(t *testing.T) {
	err := runWith(context.Background(), nil, nil, failingOpenStore)
	if err == nil || !strings.Contains(err.Error(), "open state") {
		t.Fatalf("runWith() error = %v, want wrapped open state error", err)
	}
}

func TestRunBadFlag(t *testing.T) {
	if err := run(context.Background(), []string{"-bogus"}, nil); err == nil {
		t.Fatal("expected error for bad flag")
	}
}

func TestRunListenError(t *testing.T) {
	err := run(context.Background(), []string{"-addr", "bad-addr-no-port", "-state-dir", t.TempDir()}, nil)
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("run() error = %v, want wrapped listen error", err)
	}
}

func TestRunServeImmediateError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	if err := run(context.Background(), []string{"-state-dir", t.TempDir()}, ln); err == nil {
		t.Fatal("expected error from a closed listener")
	}
}

func TestRunGracefulShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() { runErrCh <- run(ctx, []string{"-state-dir", t.TempDir()}, ln) }()

	waitHealthy(t, addr)

	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d", resp.StatusCode)
	}

	mresp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	mbody, _ := io.ReadAll(mresp.Body)
	mresp.Body.Close()
	if mresp.StatusCode != http.StatusOK || !strings.Contains(string(mbody), "infra_resources_total") {
		t.Fatalf("unexpected metrics response: status=%d body=%s", mresp.StatusCode, mbody)
	}

	cancel()

	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("run() = %v, want nil after graceful shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() did not return after context cancellation")
	}
}

func TestRunDefaultListener(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	args := []string{"-addr", "127.0.0.1:0", "-state-dir", t.TempDir()}
	go func() { runErrCh <- run(ctx, args, nil) }()

	// Give the server a moment to bind its own listener before tearing it
	// down; there is no injected listener here to poll for readiness.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("run() = %v, want nil after graceful shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() did not return after context cancellation")
	}
}

func waitHealthy(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become healthy in time", addr)
}
