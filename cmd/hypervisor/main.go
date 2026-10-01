// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Command hypervisor boots one VM under internal/hypervisor and serves
// internal/hypervisor/protocol's control socket for it: one OS process per
// VM, the same isolation granularity cloud-hypervisor gets today (see
// internal/manager/vmm.go), so a bug in this process can't take another
// VM or infra-agent down with it. It is not yet wired into microvm as a
// selectable backend — see docs/architecture/go-hypervisor.md.
package main

import (
	"flag"
	"log/slog"
	"os"
)

func main() {
	if err := run(); err != nil {
		slog.Error("hypervisor stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	sockPath := flag.String("control-socket", "", "unix socket path to serve the control protocol on (required)")
	flag.Parse()
	if *sockPath == "" {
		flag.Usage()
		os.Exit(2)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	logger.Info("hypervisor starting", "controlSocket", *sockPath)

	srv := newServer(logger)
	return srv.listenAndServe(*sockPath)
}
