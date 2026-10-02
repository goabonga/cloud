// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package crypto provides envelope encryption, the primitive behind the
// platform's KMS. A key-encryption key (KEK) wraps a fresh per-payload
// data-encryption key (DEK); only the wrapped DEK and the ciphertext are
// stored, so compromising stored data without the KEK reveals nothing.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// KeySize is the AES-256 key length in bytes.
const KeySize = 32

// Envelope is the stored form of an encrypted payload.
type Envelope struct {
	WrappedDEK []byte `json:"wrappedDek"`
	DEKNonce   []byte `json:"dekNonce"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

// KEK is a key-encryption key used to wrap per-payload data keys.
type KEK struct {
	aead cipher.AEAD
}

// NewKEK builds a KEK from a 32-byte key.
func NewKEK(key []byte) (*KEK, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("crypto: KEK must be %d bytes, got %d", KeySize, len(key))
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	return &KEK{aead: aead}, nil
}

// NewKEKFromBase64 builds a KEK from a base64-encoded 32-byte key.
func NewKEKFromBase64(s string) (*KEK, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("crypto: decode KEK: %w", err)
	}
	return NewKEK(raw)
}

// GenerateKey returns a fresh random 32-byte key suitable for a KEK.
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("crypto: generate key: %w", err)
	}
	return key, nil
}

// Encrypt wraps plaintext in a fresh envelope.
func (k *KEK) Encrypt(plaintext []byte) (*Envelope, error) {
	dek, err := GenerateKey()
	if err != nil {
		return nil, err
	}
	dataAEAD, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	nonce, err := randomNonce(dataAEAD.NonceSize())
	if err != nil {
		return nil, err
	}
	ciphertext := dataAEAD.Seal(nil, nonce, plaintext, nil)

	dekNonce, err := randomNonce(k.aead.NonceSize())
	if err != nil {
		return nil, err
	}
	wrapped := k.aead.Seal(nil, dekNonce, dek, nil)

	return &Envelope{WrappedDEK: wrapped, DEKNonce: dekNonce, Nonce: nonce, Ciphertext: ciphertext}, nil
}

// Decrypt unwraps the DEK and recovers the plaintext, failing if the KEK is
// wrong or the envelope was tampered with.
func (k *KEK) Decrypt(e *Envelope) ([]byte, error) {
	dek, err := k.aead.Open(nil, e.DEKNonce, e.WrappedDEK, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: unwrap DEK: %w", err)
	}
	dataAEAD, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	plaintext, err := dataAEAD.Open(nil, e.Nonce, e.Ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: decrypt: %w", err)
	}
	return plaintext, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new gcm: %w", err)
	}
	return aead, nil
}

func randomNonce(size int) ([]byte, error) {
	nonce := make([]byte, size)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	return nonce, nil
}
