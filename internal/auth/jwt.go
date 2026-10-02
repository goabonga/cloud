// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package auth

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
)

// JWTAuthenticator validates ES256 bearer JWTs against a public key and issuer.
type JWTAuthenticator struct {
	pub    *ecdsa.PublicKey
	issuer string
}

// NewJWTAuthenticator returns an authenticator that accepts tokens signed by the
// matching private key and carrying the given issuer claim.
func NewJWTAuthenticator(pub *ecdsa.PublicKey, issuer string) *JWTAuthenticator {
	return &JWTAuthenticator{pub: pub, issuer: issuer}
}

// claims is the JWT payload issued by internal/idp: the standard registered
// claims plus the subject's roles.
type claims struct {
	jwt.RegisteredClaims
	Roles []string `json:"roles,omitempty"`
}

// Authenticate verifies the bearer JWT and resolves its subject and roles.
func (a *JWTAuthenticator) Authenticate(r *http.Request) (*Identity, error) {
	token, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		return nil, ErrUnauthenticated
	}
	c := &claims{}
	_, err := jwt.ParseWithClaims(token, c, func(*jwt.Token) (any, error) {
		return a.pub, nil
	}, jwt.WithValidMethods([]string{"ES256"}), jwt.WithIssuer(a.issuer), jwt.WithExpirationRequired())
	if err != nil || c.Subject == "" {
		return nil, ErrUnauthenticated
	}
	return &Identity{Subject: c.Subject, Roles: c.Roles}, nil
}

// ParseECPublicKeyPEM decodes a PKIX PEM-encoded EC public key.
func ParseECPublicKeyPEM(pemBytes []byte) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("auth: invalid public key PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("auth: parse public key: %w", err)
	}
	pub, ok := parsed.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("auth: public key is not ECDSA (%T)", parsed)
	}
	return pub, nil
}
