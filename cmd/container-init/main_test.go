// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	run(&buf)

	got := buf.String()
	if !strings.HasPrefix(got, "infra-container-init ") {
		t.Fatalf("run() wrote %q, want it to start with %q", got, "infra-container-init ")
	}
	if !strings.Contains(got, Version) {
		t.Fatalf("run() wrote %q, want it to contain Version %q", got, Version)
	}
}
