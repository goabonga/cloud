// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package cli provides the bootstrap command-line interface.
package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/goabonga/cloud/internal/transport"
)

// Run parses the public CLI arguments and runs the matching command. Cobra
// supplies help, `--version` and shell completion scripts (`completion`).
func Run(ctx context.Context, args []string, output io.Writer, version string) error {
	root := command(version)
	root.SetArgs(args)
	root.SetOut(output)
	root.SetErr(output)
	return root.ExecuteContext(ctx)
}

func command(version string) *cobra.Command {
	root := &cobra.Command{
		Use:     "cloud",
		Short:   "Command-line client of the cloud control plane",
		Version: version,
		// main reports the error once; usage stays reserved for --help.
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetVersionTemplate("cloud {{.Version}}\n")
	root.AddCommand(statusCommand())
	return root
}

// statusCommand asks the running daemon for its health over its Unix socket.
func statusCommand() *cobra.Command {
	var socket string
	status := &cobra.Command{
		Use:   "status",
		Short: "Print the daemon health reported on its Unix socket",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			request, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, "http://cloud/healthz", nil)
			if err != nil {
				return err
			}
			response, err := transport.Client(socket).Do(request)
			if err != nil {
				return fmt.Errorf("daemon not reachable at %s: %w", socket, err)
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != http.StatusOK {
				return fmt.Errorf("daemon returned %s", response.Status)
			}
			_, err = io.Copy(cmd.OutOrStdout(), io.LimitReader(response.Body, 1<<16))
			return err
		},
	}
	status.Flags().StringVar(&socket, "socket", transport.DefaultSocket(), "daemon Unix socket path")
	return status
}
