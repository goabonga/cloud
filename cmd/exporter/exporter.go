// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/meta"
	"github.com/goabonga/infrastructure/internal/metrics"
	"github.com/goabonga/infrastructure/internal/state"
)

// config holds the exporter's parsed command-line configuration.
type config struct {
	addr     string
	stateDir string
	stateDSN string
}

// parseArgs parses args into a config. It uses flag.ContinueOnError so a bad
// flag is reported as an error instead of exiting the process, unlike the
// global flag package's default (flag.ExitOnError), so it can be tested.
func parseArgs(args []string) (*config, error) {
	fs := flag.NewFlagSet("infra-exporter", flag.ContinueOnError)
	addr := fs.String("addr", envOr("GOA_EXPORTER_ADDR", ":9100"), "listen address")
	stateDir := fs.String("state-dir", envOr("GOA_STATE_DIR", "./state"), "state directory")
	stateDSN := fs.String("state-dsn", envOr("GOA_STATE_DSN", ""), "PostgreSQL DSN (enables the HA backend)")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return &config{addr: *addr, stateDir: *stateDir, stateDSN: *stateDSN}, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// newMux builds the exporter's HTTP handler: a Prometheus registry scraping
// store on /metrics, and a liveness probe on /healthz. It performs no network
// I/O itself, so it is directly testable with httptest.
func newMux(store state.Store) *http.ServeMux {
	reg := prometheus.NewRegistry()
	reg.MustRegister(metrics.NewCollector(store,
		resource.KindVPC,
		resource.KindACLPolicy,
		resource.KindSecret,
		resource.KindSSLCA,
	))

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

// newServer builds the HTTP server around handler, ready to Serve a listener.
func newServer(handler http.Handler) *http.Server {
	return &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
}

// openStoreFunc opens the configured state backend. It is a seam so tests can
// exercise the "bad state configuration" error path without a real backend.
type openStoreFunc func(dir, dsn string) (state.Store, error)

// mapServeErr turns the sentinel error returned by a graceful Serve shutdown
// into a nil error; any other error (including a listen failure) passes
// through unchanged.
func mapServeErr(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// run parses args, opens the state backend, and serves the exporter's HTTP
// handler on listener until ctx is canceled or an unrecoverable serve error
// occurs. A nil listener means "listen on cfg.addr", used for normal
// operation; a test injects its own (e.g. from net.Listen("tcp",
// "127.0.0.1:0")) to exercise the real server on an ephemeral port.
func run(ctx context.Context, args []string, listener net.Listener) error {
	return runWith(ctx, args, listener, state.Open)
}

func runWith(ctx context.Context, args []string, listener net.Listener, openStore openStoreFunc) error {
	cfg, err := parseArgs(args)
	if err != nil {
		return err
	}

	store, err := openStore(cfg.stateDir, cfg.stateDSN)
	if err != nil {
		return fmt.Errorf("open state: %w", err)
	}
	defer store.Close()

	ln := listener
	if ln == nil {
		ln, err = net.Listen("tcp", cfg.addr)
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
	}
	defer ln.Close()

	srv := newServer(newMux(store))
	log.Printf("%s listening on %s (state dir %s)", meta.Line("infra-exporter", Version), ln.Addr(), cfg.stateDir)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return mapServeErr(<-errCh)
	case err := <-errCh:
		return mapServeErr(err)
	}
}
