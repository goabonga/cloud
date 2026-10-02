// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "fmt"

// KMS resource kinds.
const (
	KindKMSKeyring = "kms_keyring"
	KindKMSKey     = "kms_key"
)

// KMSKeyringSpec is the desired state of a KMS keyring (a key container).
type KMSKeyringSpec struct {
	Name string `json:"name"`
}

// Validate reports whether the spec is well-formed.
func (s KMSKeyringSpec) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("kms_keyring: name is required")
	}
	return nil
}

// KMSKeyringStatus is the observed state of a keyring.
type KMSKeyringStatus struct {
	StatusBase
}

// KMSKeyring is a KMS keyring resource.
type KMSKeyring = Resource[KMSKeyringSpec, KMSKeyringStatus]

// KMSKeySpec is the desired state of a KMS key used to encrypt disks/secrets.
type KMSKeySpec struct {
	KeyringID string `json:"keyringId"`
	Name      string `json:"name"`
	// Purpose, Algorithm and RotationPeriod are advisory metadata.
	Purpose        string `json:"purpose,omitempty"`
	Algorithm      string `json:"algorithm,omitempty"`
	RotationPeriod string `json:"rotationPeriod,omitempty"`
}

// Validate reports whether the spec is well-formed.
func (s KMSKeySpec) Validate() error {
	if s.KeyringID == "" {
		return fmt.Errorf("kms_key: keyringId is required")
	}
	if s.Name == "" {
		return fmt.Errorf("kms_key: name is required")
	}
	return nil
}

// KMSKeyStatus is the observed state of a key.
type KMSKeyStatus struct {
	StatusBase
	ActiveVersion int `json:"activeVersion,omitempty"`
}

// KMSKey is a KMS key resource.
type KMSKey = Resource[KMSKeySpec, KMSKeyStatus]
