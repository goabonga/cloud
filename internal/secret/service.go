// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package secret stores secrets encrypted at rest. Plaintext is encrypted with
// the KEK on write and never persisted or returned in clear; a deliberate
// Reveal call is the only way to recover it.
package secret

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/goabonga/infrastructure/internal/crypto"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
)

// Registry is the typed store for secrets.
type Registry = registry.Registry[resource.SecretSpec, resource.SecretStatus]

// VersionRegistry is the typed store for secret versions.
type VersionRegistry = registry.Registry[resource.SecretVersionSpec, resource.SecretVersionStatus]

// Service encrypts secrets with a KEK and stores them in a registry.
type Service struct {
	reg      *Registry
	versions *VersionRegistry
	kek      *crypto.KEK
	now      func() time.Time
}

// NewService returns a secret Service backed by reg (secrets), versions and kek.
func NewService(reg *Registry, versions *VersionRegistry, kek *crypto.KEK) *Service {
	return &Service{reg: reg, versions: versions, kek: kek, now: time.Now}
}

// Put encrypts plaintext and stores the secret under uid, returning the
// redacted resource (no plaintext, no ciphertext).
func (s *Service) Put(uid, name, plaintext string) (*resource.Secret, error) {
	if uid == "" {
		return nil, fmt.Errorf("secret: uid is required")
	}
	if plaintext == "" {
		return nil, fmt.Errorf("secret: data is required")
	}
	env, err := s.kek.Encrypt([]byte(plaintext))
	if err != nil {
		return nil, err
	}
	blob, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("secret: marshal envelope: %w", err)
	}

	sec := &resource.Secret{Metadata: resource.ObjectMeta{UID: uid, Name: name}}
	if existing, err := s.reg.Get(uid); err == nil {
		sec.Metadata.ResourceVersion = existing.Metadata.ResourceVersion
		sec.Metadata.CreatedAt = existing.Metadata.CreatedAt
		sec.Metadata.Generation = existing.Metadata.Generation + 1
	} else {
		sec.Metadata.CreatedAt = s.now()
		sec.Metadata.Generation = 1
	}
	sec.Status.Ciphertext = blob
	sec.Status.MarkReconciled(sec.Metadata.Generation)
	sec.Status.SetPhase(resource.PhaseReady, "Encrypted", "stored encrypted at rest")

	if err := s.reg.Put(sec); err != nil {
		return nil, err
	}
	return redact(sec), nil
}

// Get returns the redacted secret (metadata and status, no secret material).
func (s *Service) Get(uid string) (*resource.Secret, error) {
	sec, err := s.reg.Get(uid)
	if err != nil {
		return nil, err
	}
	return redact(sec), nil
}

// List returns every secret, redacted.
func (s *Service) List() ([]resource.Secret, error) {
	secs, err := s.reg.List()
	if err != nil {
		return nil, err
	}
	for i := range secs {
		secs[i] = *redact(&secs[i])
	}
	return secs, nil
}

// Reveal decrypts and returns the plaintext for uid.
func (s *Service) Reveal(uid string) (string, error) {
	sec, err := s.reg.Get(uid)
	if err != nil {
		return "", err
	}
	var env crypto.Envelope
	if err := json.Unmarshal(sec.Status.Ciphertext, &env); err != nil {
		return "", fmt.Errorf("secret: unmarshal envelope: %w", err)
	}
	plaintext, err := s.kek.Decrypt(&env)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// Delete removes the secret.
func (s *Service) Delete(uid string) error {
	return s.reg.Delete(uid)
}

// redact returns a copy with all secret material stripped.
func redact(sec *resource.Secret) *resource.Secret {
	out := *sec
	out.Spec.Data = ""
	out.Status.Ciphertext = nil
	return &out
}

// AddVersion encrypts plaintext and stores it as a new version of secretID under
// uid, assigning the next sequential version number. Returns the redacted
// version (no plaintext, no ciphertext).
func (s *Service) AddVersion(uid, secretID, plaintext string) (*resource.SecretVersion, error) {
	if uid == "" {
		return nil, fmt.Errorf("secret_version: uid is required")
	}
	if secretID == "" {
		return nil, fmt.Errorf("secret_version: secretId is required")
	}
	if plaintext == "" {
		return nil, fmt.Errorf("secret_version: data is required")
	}
	env, err := s.kek.Encrypt([]byte(plaintext))
	if err != nil {
		return nil, err
	}
	blob, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("secret_version: marshal envelope: %w", err)
	}

	next, err := s.nextVersion(secretID)
	if err != nil {
		return nil, err
	}

	ver := &resource.SecretVersion{
		Metadata: resource.ObjectMeta{UID: uid, CreatedAt: s.now(), Generation: 1},
		Spec:     resource.SecretVersionSpec{SecretID: secretID},
	}
	ver.Status.Version = next
	ver.Status.Ciphertext = blob
	ver.Status.MarkReconciled(1)
	ver.Status.SetPhase(resource.PhaseReady, "Encrypted", "stored encrypted at rest")

	if err := s.versions.Put(ver); err != nil {
		return nil, err
	}
	return redactVersion(ver), nil
}

// ListVersions returns the redacted versions of secretID ordered by version. An
// empty secretID returns every version.
func (s *Service) ListVersions(secretID string) ([]resource.SecretVersion, error) {
	all, err := s.versions.List()
	if err != nil {
		return nil, err
	}
	out := make([]resource.SecretVersion, 0, len(all))
	for i := range all {
		if secretID != "" && all[i].Spec.SecretID != secretID {
			continue
		}
		out = append(out, *redactVersion(&all[i]))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Status.Version < out[j].Status.Version })
	return out, nil
}

// GetVersion returns the redacted version stored under uid.
func (s *Service) GetVersion(uid string) (*resource.SecretVersion, error) {
	ver, err := s.versions.Get(uid)
	if err != nil {
		return nil, err
	}
	return redactVersion(ver), nil
}

// RevealVersion decrypts and returns the plaintext of the version under uid.
func (s *Service) RevealVersion(uid string) (string, error) {
	ver, err := s.versions.Get(uid)
	if err != nil {
		return "", err
	}
	var env crypto.Envelope
	if err := json.Unmarshal(ver.Status.Ciphertext, &env); err != nil {
		return "", fmt.Errorf("secret_version: unmarshal envelope: %w", err)
	}
	plaintext, err := s.kek.Decrypt(&env)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// DeleteVersion removes the version under uid.
func (s *Service) DeleteVersion(uid string) error {
	return s.versions.Delete(uid)
}

// nextVersion returns one more than the highest version recorded for secretID.
func (s *Service) nextVersion(secretID string) (int, error) {
	all, err := s.versions.List()
	if err != nil {
		return 0, err
	}
	max := 0
	for i := range all {
		if all[i].Spec.SecretID == secretID && all[i].Status.Version > max {
			max = all[i].Status.Version
		}
	}
	return max + 1, nil
}

func redactVersion(ver *resource.SecretVersion) *resource.SecretVersion {
	out := *ver
	out.Spec.Data = ""
	out.Status.Ciphertext = nil
	return &out
}
