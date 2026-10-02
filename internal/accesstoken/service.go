// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package accesstoken issues and verifies revocable credentials that act as
// their owning user. Unlike a password or a secret, a token's plaintext is
// server-generated and never persisted: only its SHA-256 hash is stored, and
// the plaintext is returned exactly once, at creation.
package accesstoken

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/identity"
	"github.com/goabonga/infrastructure/internal/registry"
)

// ErrInvalid is returned by Introspect for a token that is unknown, expired,
// or whose owner no longer exists or is disabled. It deliberately does not
// distinguish between these.
var ErrInvalid = errors.New("accesstoken: invalid token")

// Registry is the typed store for access tokens.
type Registry = registry.Registry[resource.AccessTokenSpec, resource.AccessTokenStatus]

// Service manages access tokens backed by a registry, resolving their
// owner's current roles from users.
type Service struct {
	reg   *Registry
	users *identity.Service
	now   func() time.Time
}

// NewService returns a Service backed by reg, resolving owners through users.
func NewService(reg *Registry, users *identity.Service) *Service {
	return &Service{reg: reg, users: users, now: time.Now}
}

// Put creates or updates the access token under uid, owned by ownerUID. On
// create it generates a new token and returns its plaintext; on update it
// only changes Name/ExpiresAt, never regenerates the token, and the returned
// plaintext is empty.
func (s *Service) Put(uid, ownerUID string, spec resource.AccessTokenSpec) (*resource.AccessToken, string, error) {
	if uid == "" {
		return nil, "", fmt.Errorf("accesstoken: uid is required")
	}
	if err := spec.Validate(); err != nil {
		return nil, "", err
	}

	at := &resource.AccessToken{Metadata: resource.ObjectMeta{UID: uid, Name: spec.Name}, Spec: spec}

	existing, err := s.reg.Get(uid)
	var plaintext string
	switch err {
	case nil:
		at.Metadata.ResourceVersion = existing.Metadata.ResourceVersion
		at.Metadata.CreatedAt = existing.Metadata.CreatedAt
		at.Metadata.Generation = existing.Metadata.Generation + 1
		at.Status = existing.Status
	default:
		at.Metadata.CreatedAt = s.now()
		at.Metadata.Generation = 1
		plaintext, err = generateToken()
		if err != nil {
			return nil, "", err
		}
		hash := sha256.Sum256([]byte(plaintext))
		at.Status.OwnerUID = ownerUID
		at.Status.TokenHash = hash[:]
	}
	at.Status.MarkReconciled(at.Metadata.Generation)
	at.Status.SetPhase(resource.PhaseReady, "Stored", "access token stored")

	if err := s.reg.Put(at); err != nil {
		return nil, "", err
	}
	return redact(at), plaintext, nil
}

// Get returns the redacted access token.
func (s *Service) Get(uid string) (*resource.AccessToken, error) {
	at, err := s.reg.Get(uid)
	if err != nil {
		return nil, err
	}
	return redact(at), nil
}

// List returns every access token, redacted.
func (s *Service) List() ([]resource.AccessToken, error) {
	tokens, err := s.reg.List()
	if err != nil {
		return nil, err
	}
	for i := range tokens {
		tokens[i] = *redact(&tokens[i])
	}
	return tokens, nil
}

// Delete revokes the access token.
func (s *Service) Delete(uid string) error {
	return s.reg.Delete(uid)
}

// Introspect verifies plaintext and returns the current UID and roles of the
// token's owner, looked up live rather than cached from creation time: a
// demoted or disabled owner invalidates every token they hold immediately.
func (s *Service) Introspect(plaintext string) (subject string, roles []string, err error) {
	if plaintext == "" {
		return "", nil, ErrInvalid
	}
	hash := sha256.Sum256([]byte(plaintext))

	tokens, err := s.reg.List()
	if err != nil {
		return "", nil, err
	}
	for i := range tokens {
		if subtle.ConstantTimeCompare(tokens[i].Status.TokenHash, hash[:]) != 1 {
			continue
		}
		if exp := tokens[i].Spec.ExpiresAt; exp != nil && s.now().After(*exp) {
			return "", nil, ErrInvalid
		}
		owner, err := s.users.Get(tokens[i].Status.OwnerUID)
		if err != nil || owner.Spec.Disabled {
			return "", nil, ErrInvalid
		}
		return owner.Metadata.UID, owner.Spec.Roles, nil
	}
	return "", nil, ErrInvalid
}

// redact returns a copy with the token hash stripped. There is no
// Spec-side secret to clear: the plaintext is server-generated, never
// client-supplied.
func redact(at *resource.AccessToken) *resource.AccessToken {
	out := *at
	out.Status.TokenHash = nil
	return &out
}

// generateToken returns a high-entropy, URL-safe token with a recognizable
// prefix, so an accidental leak is easy to grep for.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("accesstoken: generate token: %w", err)
	}
	return "infra_" + base64.RawURLEncoding.EncodeToString(b), nil
}
