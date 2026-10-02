// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"strings"
	"testing"
)

func TestBridgeName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		uid  string
		want string
	}{
		{"short", "vpc-1", "br-vpc-1"},
		{"lowercased", "VPC-AB", "br-vpc-ab"},
		{"strips invalid", "vpc_1!", "br-vpc1"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := bridgeName(tc.uid); got != tc.want {
				t.Fatalf("bridgeName(%q) = %q, want %q", tc.uid, got, tc.want)
			}
		})
	}
}

func TestBridgeNameHashesLongUID(t *testing.T) {
	t.Parallel()

	long := "vpc-this-is-a-very-long-identifier"
	got := bridgeName(long)
	if len(got) > maxIfaceName {
		t.Fatalf("bridgeName(%q) = %q (len %d) exceeds %d", long, got, len(got), maxIfaceName)
	}
	if !strings.HasPrefix(got, "br-") {
		t.Fatalf("bridgeName(%q) = %q, want br- prefix", long, got)
	}
	// Deterministic.
	if again := bridgeName(long); again != got {
		t.Fatalf("bridgeName not deterministic: %q vs %q", got, again)
	}
}
