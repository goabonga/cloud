// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package crypto

import (
	"crypto/hkdf"
	"crypto/sha256"
	"fmt"
)

// DeriveKey derives a length-byte subkey from master and a context string using
// HKDF-SHA256. It is used to turn the master KMS key into per-resource keys
// (for example, a dm-crypt passphrase per disk) without storing extra material.
func DeriveKey(master []byte, info string, length int) ([]byte, error) {
	if len(master) == 0 {
		return nil, fmt.Errorf("crypto: empty master key")
	}
	key, err := hkdf.Key(sha256.New, master, nil, info, length)
	if err != nil {
		return nil, fmt.Errorf("crypto: derive key: %w", err)
	}
	return key, nil
}
