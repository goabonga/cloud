// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package main

import (
	"testing"
	"time"
)

func TestCredentialsRoundtrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if _, ok := loadCredentials(); ok {
		t.Fatal("expected no credentials before saving any")
	}

	want := credentials{Token: "a-token", ExpiresAt: time.Now().Add(time.Hour)}
	if err := saveCredentials(want); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, ok := loadCredentials()
	if !ok || got.Token != want.Token {
		t.Fatalf("loaded %+v, want %+v", got, want)
	}
}

func TestLoadCredentialsIgnoresExpired(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := saveCredentials(credentials{Token: "a-token", ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, ok := loadCredentials(); ok {
		t.Fatal("expected an expired credential to be ignored")
	}
}
