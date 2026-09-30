// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "fmt"

// KindUser is the resource kind for users.
const KindUser = "user"

// UserSpec is the desired state of a user. Password carries the plaintext on
// write only; it is never persisted or returned in clear.
type UserSpec struct {
	Username string   `json:"username"`
	Password string   `json:"password,omitempty"`
	Roles    []string `json:"roles,omitempty"`
	Disabled bool     `json:"disabled,omitempty"`
}

// Validate reports whether the spec is well-formed for a write.
func (s UserSpec) Validate() error {
	if s.Username == "" {
		return fmt.Errorf("user: username is required")
	}
	return nil
}

// UserStatus is the observed state of a user. PasswordHash is the bcrypt
// digest stored at rest.
type UserStatus struct {
	StatusBase
	PasswordHash []byte `json:"passwordHash,omitempty"`
}

// User is an identity resource authenticated by password.
type User = Resource[UserSpec, UserStatus]
