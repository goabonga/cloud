// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Command infra-lb is the load balancers' data plane: it serves the listeners
// and target groups of a JSON configuration file, reloading it when it changes
// or on SIGHUP, and reports its listeners and the health of its targets in a
// JSON status file. See internal/lbproxy.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/goabonga/infrastructure/internal/lbproxy"
	"github.com/goabonga/infrastructure/internal/meta"
)

func main() {
	if err := run(); err != nil {
		slog.Error("infra-lb stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	config := flag.String("config", envOr("GOA_LB_CONFIG", "/etc/infra/lb.json"), "configuration file")
	status := flag.String("status", envOr("GOA_LB_STATUS", ""), "status file, rewritten every second (empty: none)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)
	logger.Info(meta.Line("infra-lb", Version), "config", *config, "status", *status)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	reload := make(chan struct{}, 1)
	go func() {
		for range hup {
			select {
			case reload <- struct{}{}:
			default:
			}
		}
	}()

	return lbproxy.Run(ctx, lbproxy.New(logger), lbproxy.RunOptions{
		ConfigPath: *config,
		StatusPath: *status,
		Reload:     reload,
	})
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
