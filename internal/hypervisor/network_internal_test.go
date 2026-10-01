// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package hypervisor

import "testing"

func TestParseMAC(t *testing.T) {
	mac, err := parseMAC("02:11:22:33:44:55")
	if err != nil {
		t.Fatalf("parseMAC: %v", err)
	}
	want := [6]byte{0x02, 0x11, 0x22, 0x33, 0x44, 0x55}
	if mac != want {
		t.Errorf("parseMAC = %v, want %v", mac, want)
	}
}

func TestParseMACRejectsMalformed(t *testing.T) {
	if _, err := parseMAC("not-a-mac"); err == nil {
		t.Error("parseMAC succeeded on garbage input, want error")
	}
}

func TestParseMACRejectsEUI64(t *testing.T) {
	// An 8-byte EUI-64 address: valid to net.ParseMAC, but too long for
	// a virtio-net config space's 6-byte mac field.
	if _, err := parseMAC("02:11:22:33:44:55:66:77"); err == nil {
		t.Error("parseMAC succeeded on an 8-byte EUI-64 address, want error")
	}
}
