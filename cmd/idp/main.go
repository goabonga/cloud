// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Command cloud-identity-platform provides its bootstrap HTTP health endpoint.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/goabonga/cloud/internal/transport"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := transport.Run(ctx, os.Args[1:], os.Stdout, "cloud-identity-platform", Version, "127.0.0.1:8081", transport.Health("cloud-identity-platform", Version)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
