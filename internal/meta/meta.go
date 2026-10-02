// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package meta provides shared identity helpers for the infrastructure
// binaries. It is the smallest member of the shared core (internal/domain,
// internal/state, internal/meta) that downstream components depend on.
package meta

import "fmt"

// Line returns the one-line identity banner printed by each component binary,
// e.g. "infra-api 0.0.0".
func Line(name, version string) string {
	return fmt.Sprintf("%s %s", name, version)
}
