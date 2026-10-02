// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package cli provides the bootstrap command-line interface.
package cli

import (
	"flag"
	"fmt"
	"io"
)

// Run parses the public CLI flags and writes its version or help.
func Run(args []string, output io.Writer, version string) error {
	flags := flag.NewFlagSet("cloud", flag.ContinueOnError)
	flags.SetOutput(output)
	showVersion := flags.Bool("version", false, "print version")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unknown command: %s", flags.Arg(0))
	}
	if *showVersion {
		_, err := fmt.Fprintf(output, "cloud %s\n", version)
		return err
	}
	flags.Usage()
	return nil
}
