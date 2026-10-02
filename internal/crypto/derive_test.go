// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package crypto_test

import (
	"bytes"
	"testing"

	"github.com/goabonga/infrastructure/internal/crypto"
)

func TestDeriveKey(t *testing.T) {
	t.Parallel()

	master, _ := crypto.GenerateKey()

	a, err := crypto.DeriveKey(master, "disk:key-1:disk-1", 32)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if len(a) != 32 {
		t.Fatalf("len = %d, want 32", len(a))
	}

	// Deterministic for the same context.
	again, _ := crypto.DeriveKey(master, "disk:key-1:disk-1", 32)
	if !bytes.Equal(a, again) {
		t.Fatal("derivation should be deterministic")
	}

	// Different context yields a different key.
	b, _ := crypto.DeriveKey(master, "disk:key-1:disk-2", 32)
	if bytes.Equal(a, b) {
		t.Fatal("different context should derive a different key")
	}

	if _, err := crypto.DeriveKey(nil, "x", 32); err == nil {
		t.Fatal("expected error for empty master")
	}
}
