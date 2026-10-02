// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package meta_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/meta"
)

func TestLine(t *testing.T) {
	t.Parallel()

	got := meta.Line("infra-api", "1.2.3")
	want := "infra-api 1.2.3"
	if got != want {
		t.Fatalf("Line(%q, %q) = %q, want %q", "infra-api", "1.2.3", got, want)
	}
}
