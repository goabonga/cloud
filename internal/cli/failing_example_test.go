// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package cli

import "testing"

// TestFailingExample is a pipeline test: it always fails.
func TestFailingExample(t *testing.T) {
	if 1+1 != 3 {
		t.Fatalf("1+1 = %d, want 3", 1+1)
	}
}
