// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package crypto_test

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/goabonga/infrastructure/internal/crypto"
)

func newKEK(t *testing.T) *crypto.KEK {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	kek, err := crypto.NewKEK(key)
	if err != nil {
		t.Fatalf("new kek: %v", err)
	}
	return kek
}

func TestEnvelopeRoundtrip(t *testing.T) {
	t.Parallel()

	kek := newKEK(t)
	plaintext := []byte("s3cr3t-p@ssw0rd")

	env, err := kek.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Contains(env.Ciphertext, plaintext) {
		t.Fatal("ciphertext leaks plaintext")
	}

	got, err := kek.Decrypt(env)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("got %q, want %q", got, plaintext)
	}
}

func TestEnvelopeWrongKEKFails(t *testing.T) {
	t.Parallel()

	env, err := newKEK(t).Encrypt([]byte("data"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := newKEK(t).Decrypt(env); err == nil {
		t.Fatal("decrypt with a different KEK should fail")
	}
}

func TestEnvelopeTamperFails(t *testing.T) {
	t.Parallel()

	kek := newKEK(t)
	env, err := kek.Encrypt([]byte("data"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	env.Ciphertext[0] ^= 0xff
	if _, err := kek.Decrypt(env); err == nil {
		t.Fatal("tampered ciphertext should fail authentication")
	}
}

func TestNewKEKRejectsWrongSize(t *testing.T) {
	t.Parallel()

	if _, err := crypto.NewKEK([]byte("too-short")); err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestNewKEKFromBase64(t *testing.T) {
	t.Parallel()

	key, _ := crypto.GenerateKey()
	if _, err := crypto.NewKEKFromBase64(base64.StdEncoding.EncodeToString(key)); err != nil {
		t.Fatalf("from base64: %v", err)
	}
	if _, err := crypto.NewKEKFromBase64("!!not-base64!!"); err == nil {
		t.Fatal("expected error for invalid base64")
	}
}
