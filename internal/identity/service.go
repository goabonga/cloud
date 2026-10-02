// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package identity stores users authenticated by password. Passwords are
// hashed with bcrypt on write and never persisted or returned in clear.
package identity

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// ErrInvalidCredentials is returned by Authenticate for an unknown user, a
// wrong password, or a disabled account. It deliberately does not distinguish
// between these so a caller cannot use it to enumerate usernames.
var ErrInvalidCredentials = errors.New("identity: invalid credentials")

// Registry is the typed store for users.
type Registry = registry.Registry[resource.UserSpec, resource.UserStatus]

// Service manages users backed by a registry.
type Service struct {
	reg *Registry
	now func() time.Time
}

// NewService returns a user Service backed by reg.
func NewService(reg *Registry) *Service {
	return &Service{reg: reg, now: time.Now}
}

// Put creates or updates the user under uid. A non-empty spec.Password is
// hashed into the status and never persisted in the spec; an empty password on
// update leaves the existing hash untouched.
func (s *Service) Put(uid string, spec resource.UserSpec) (*resource.User, error) {
	if uid == "" {
		return nil, fmt.Errorf("identity: uid is required")
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	usr := &resource.User{Metadata: resource.ObjectMeta{UID: uid, Name: spec.Username}, Spec: spec}

	existing, err := s.reg.Get(uid)
	switch {
	case err == nil:
		usr.Metadata.ResourceVersion = existing.Metadata.ResourceVersion
		usr.Metadata.CreatedAt = existing.Metadata.CreatedAt
		usr.Metadata.Generation = existing.Metadata.Generation + 1
		usr.Status = existing.Status
	case errors.Is(err, state.ErrNotFound):
		if spec.Password == "" {
			return nil, fmt.Errorf("identity: password is required to create a user")
		}
		usr.Metadata.CreatedAt = s.now()
		usr.Metadata.Generation = 1
	default:
		return nil, err
	}

	if spec.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(spec.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("identity: hash password: %w", err)
		}
		usr.Status.PasswordHash = hash
	}
	usr.Spec.Password = ""
	usr.Status.MarkReconciled(usr.Metadata.Generation)
	usr.Status.SetPhase(resource.PhaseReady, "Stored", "user record stored")

	if err := s.reg.Put(usr); err != nil {
		return nil, err
	}
	return redact(usr), nil
}

// Get returns the redacted user (no password material).
func (s *Service) Get(uid string) (*resource.User, error) {
	usr, err := s.reg.Get(uid)
	if err != nil {
		return nil, err
	}
	return redact(usr), nil
}

// List returns every user, redacted.
func (s *Service) List() ([]resource.User, error) {
	users, err := s.reg.List()
	if err != nil {
		return nil, err
	}
	for i := range users {
		users[i] = *redact(&users[i])
	}
	return users, nil
}

// Delete removes the user.
func (s *Service) Delete(uid string) error {
	return s.reg.Delete(uid)
}

// Authenticate verifies username and password, returning the redacted user on
// success. It fails closed for an unknown username, a wrong password, or a
// disabled account, all with ErrInvalidCredentials.
func (s *Service) Authenticate(username, password string) (*resource.User, error) {
	users, err := s.reg.List()
	if err != nil {
		return nil, err
	}
	for i := range users {
		if users[i].Spec.Username != username {
			continue
		}
		if users[i].Spec.Disabled {
			return nil, ErrInvalidCredentials
		}
		if bcrypt.CompareHashAndPassword(users[i].Status.PasswordHash, []byte(password)) != nil {
			return nil, ErrInvalidCredentials
		}
		return redact(&users[i]), nil
	}
	return nil, ErrInvalidCredentials
}

// redact returns a copy with all password material stripped.
func redact(usr *resource.User) *resource.User {
	out := *usr
	out.Spec.Password = ""
	out.Status.PasswordHash = nil
	return &out
}
