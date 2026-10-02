// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Command cloud provides the bootstrap command-line interface.
package main

import (
	"fmt"
	"os"

	"github.com/goabonga/cloud/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:], os.Stdout, Version); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
