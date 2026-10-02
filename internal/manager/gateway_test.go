// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import "testing"

func TestGatewayCIDR(t *testing.T) {
	t.Parallel()

	tests := []struct {
		cidr string
		want string
	}{
		{"10.0.1.0/24", "10.0.1.1/24"},
		{"10.0.0.0/16", "10.0.0.1/16"},
		{"192.168.5.0/24", "192.168.5.1/24"},
	}
	for _, tc := range tests {
		got, err := gatewayCIDR(tc.cidr)
		if err != nil {
			t.Fatalf("gatewayCIDR(%q): %v", tc.cidr, err)
		}
		if got != tc.want {
			t.Fatalf("gatewayCIDR(%q) = %q, want %q", tc.cidr, got, tc.want)
		}
	}
	if _, err := gatewayCIDR("not-a-cidr"); err == nil {
		t.Fatal("expected error for invalid cidr")
	}
}

func TestHostOf(t *testing.T) {
	t.Parallel()

	if got := hostOf("10.0.1.1/24"); got != "10.0.1.1" {
		t.Fatalf("hostOf = %q, want 10.0.1.1", got)
	}
	if got := hostOf("garbage"); got != "garbage" {
		t.Fatalf("hostOf passthrough = %q", got)
	}
}
