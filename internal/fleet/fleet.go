// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package fleet provides the process entry points for Fleet controller and agent.
package fleet

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/goabonga/cloud/internal/transport"
)

// Run selects a Fleet mode and starts its process health server.
func Run(ctx context.Context, args []string, output io.Writer, version string) error {
	if len(args) > 0 {
		mode := args[0]
		addr := ""
		switch mode {
		case "controller":
			addr = "127.0.0.1:8090"
		case "agent":
			addr = "127.0.0.1:8091"
		}
		if addr != "" {
			return transport.Run(ctx, args[1:], output, "cloud-fleet "+mode, version, addr,
				transport.Health("cloud-fleet-"+mode, version))
		}
	}
	flags := flag.NewFlagSet("cloud-fleet", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: cloud-fleet <controller|agent> [--addr address]\n       cloud-fleet --version")
		fmt.Fprintln(output, "Modes: controller, agent")
		flags.PrintDefaults()
	}
	showVersion := flags.Bool("version", false, "print version")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unknown Fleet mode: %s", flags.Arg(0))
	}
	if *showVersion {
		_, err := fmt.Fprintf(output, "cloud-fleet %s\n", version)
		return err
	}
	flags.Usage()
	return nil
}
