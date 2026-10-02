// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Command cloud-ssr serves the built JavaScript application.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/goabonga/cloud/internal/ssr"
	"github.com/goabonga/cloud/internal/transport"
)

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("cloud-ssr", flag.ContinueOnError)
	addr := flags.String("addr", "127.0.0.1:8088", "listen address")
	assets := flags.String("assets", "www/dist", "frontend build directory")
	version := flags.Bool("version", false, "print version")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected argument: %s", flags.Arg(0))
	}
	if *version {
		fmt.Printf("cloud-ssr %s\n", Version)
		return nil
	}
	handler, err := ssr.New(os.DirFS(*assets), Version)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	return transport.Serve(ctx, listener, handler)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
