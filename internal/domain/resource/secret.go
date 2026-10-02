// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "fmt"

// KindSecret is the resource kind for secrets.
const KindSecret = "secret"

// SecretSpec is the desired state of a secret. Data carries the plaintext on
// write only; it is never persisted or returned in clear.
type SecretSpec struct {
	Data string `json:"data,omitempty"`
}

// Validate reports whether the spec is well-formed for a write.
func (s SecretSpec) Validate() error {
	if s.Data == "" {
		return fmt.Errorf("secret: data is required")
	}
	return nil
}

// SecretStatus is the observed state of a secret. Ciphertext is the envelope
// (encrypted) representation stored at rest.
type SecretStatus struct {
	StatusBase
	Ciphertext []byte `json:"ciphertext,omitempty"`
}

// Secret is an encrypted-at-rest secret resource.
type Secret = Resource[SecretSpec, SecretStatus]

// KindSecretVersion is the resource kind for secret versions.
const KindSecretVersion = "secret_version"

// SecretVersionSpec is one immutable, encrypted-at-rest version of a secret's
// data. Data carries the plaintext on write only.
type SecretVersionSpec struct {
	SecretID string `json:"secretId"`
	Data     string `json:"data,omitempty"`
}

// Validate reports whether the spec is well-formed for a write.
func (s SecretVersionSpec) Validate() error {
	if s.SecretID == "" {
		return fmt.Errorf("secret_version: secretId is required")
	}
	if s.Data == "" {
		return fmt.Errorf("secret_version: data is required")
	}
	return nil
}

// SecretVersionStatus is the observed state of a secret version.
type SecretVersionStatus struct {
	StatusBase
	Version    int    `json:"version,omitempty"`
	Ciphertext []byte `json:"ciphertext,omitempty"`
}

// SecretVersion is one encrypted version of a secret.
type SecretVersion = Resource[SecretVersionSpec, SecretVersionStatus]
