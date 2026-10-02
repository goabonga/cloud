// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package idp

import (
	"crypto/ecdsa"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Issuer signs ES256 JWTs for authenticated subjects.
type Issuer struct {
	key    *ecdsa.PrivateKey
	issuer string
	ttl    time.Duration
}

// NewIssuer returns an Issuer signing with key, stamping the issuer claim and a
// ttl-bounded expiry.
func NewIssuer(key *ecdsa.PrivateKey, issuer string, ttl time.Duration) *Issuer {
	return &Issuer{key: key, issuer: issuer, ttl: ttl}
}

// TTL is the lifetime of issued tokens.
func (i *Issuer) TTL() time.Duration {
	return i.ttl
}

// claims is the signed token payload: the standard registered claims plus the
// subject's roles.
type claims struct {
	jwt.RegisteredClaims
	Roles []string `json:"roles,omitempty"`
}

// Issue signs a token for subject, carrying roles.
func (i *Issuer) Issue(subject string, roles []string) (string, error) {
	now := time.Now()
	c := claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			Issuer:    i.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(i.ttl)),
		},
		Roles: roles,
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodES256, c).SignedString(i.key)
	if err != nil {
		return "", fmt.Errorf("idp: sign token: %w", err)
	}
	return token, nil
}
