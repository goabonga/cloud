// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import (
	"fmt"
	"time"
)

// KindAccessToken is the resource kind for access tokens.
const KindAccessToken = "access_token"

// AccessTokenSpec is the desired state of an access token.
type AccessTokenSpec struct {
	Name      string     `json:"name"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// Validate reports whether the spec is well-formed for a write.
func (s AccessTokenSpec) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("access_token: name is required")
	}
	return nil
}

// AccessTokenStatus is the observed state of an access token. TokenHash is the
// SHA-256 digest of the plaintext, which is never persisted or returned in
// clear after creation.
type AccessTokenStatus struct {
	StatusBase
	OwnerUID  string `json:"ownerUid,omitempty"`
	TokenHash []byte `json:"tokenHash,omitempty"`
}

// AccessToken is a revocable credential acting as its owning user.
type AccessToken = Resource[AccessTokenSpec, AccessTokenStatus]
