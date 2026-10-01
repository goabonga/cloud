// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Command infra-container-init is a placeholder entry point scaffolded in M0. The real
// implementation lands in later milestones (see PLAN.md).
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/goabonga/infrastructure/internal/meta"
)

func main() {
	run(os.Stdout)
}

func run(stdout io.Writer) {
	_, _ = fmt.Fprintln(stdout, meta.Line("infra-container-init", Version))
}
