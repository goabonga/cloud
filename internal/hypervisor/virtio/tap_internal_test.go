// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package virtio

import (
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestTapIfreq(t *testing.T) {
	ifr, err := tapIfreq("tap-abc123")
	if err != nil {
		t.Fatalf("tapIfreq: %v", err)
	}
	if got := ifr.Name(); got != "tap-abc123" {
		t.Errorf("Name() = %q, want %q", got, "tap-abc123")
	}
	want := uint16(unix.IFF_TAP | unix.IFF_NO_PI)
	if got := ifr.Uint16(); got != want {
		t.Errorf("Uint16() (flags) = 0x%x, want 0x%x (IFF_TAP|IFF_NO_PI)", got, want)
	}
}

func TestTapIfreqRejectsOverlongName(t *testing.T) {
	// IFNAMSIZ is 16 including the trailing NUL; 16 ordinary characters
	// leaves no room for it.
	_, err := tapIfreq(strings.Repeat("a", 16))
	if err == nil {
		t.Fatal("tapIfreq succeeded with a 16-byte name, want error (no room for the NUL terminator)")
	}
}
