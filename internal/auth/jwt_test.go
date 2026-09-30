// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package auth_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/goabonga/infrastructure/internal/auth"
)

func mintToken(t *testing.T, key *ecdsa.PrivateKey, subject, issuer string, exp time.Time) string {
	t.Helper()
	return mintTokenWithRoles(t, key, subject, issuer, exp, nil)
}

func mintTokenWithRoles(t *testing.T, key *ecdsa.PrivateKey, subject, issuer string, exp time.Time, roles []string) string {
	t.Helper()
	claims := struct {
		jwt.RegisteredClaims
		Roles []string `json:"roles,omitempty"`
	}{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   subject,
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
		Roles: roles,
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodES256, claims).SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

func TestJWTAuthenticator(t *testing.T) {
	t.Parallel()

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	const issuer = "http://idp"
	a := auth.NewJWTAuthenticator(&key.PublicKey, issuer)

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		tok := mintToken(t, key, "alice", issuer, time.Now().Add(time.Hour))
		id, err := a.Authenticate(reqWithToken(tok))
		if err != nil {
			t.Fatalf("authenticate: %v", err)
		}
		if id.Subject != "alice" {
			t.Fatalf("subject = %q", id.Subject)
		}
	})

	t.Run("carries roles", func(t *testing.T) {
		t.Parallel()
		tok := mintTokenWithRoles(t, key, "alice", issuer, time.Now().Add(time.Hour), []string{"admin", "operator"})
		id, err := a.Authenticate(reqWithToken(tok))
		if err != nil {
			t.Fatalf("authenticate: %v", err)
		}
		if !id.HasRole("admin") || !id.HasRole("operator") || id.HasRole("nope") {
			t.Fatalf("roles = %v", id.Roles)
		}
	})

	t.Run("wrong issuer", func(t *testing.T) {
		t.Parallel()
		tok := mintToken(t, key, "alice", "http://evil", time.Now().Add(time.Hour))
		if _, err := a.Authenticate(reqWithToken(tok)); err == nil {
			t.Fatal("expected issuer mismatch to fail")
		}
	})

	t.Run("expired", func(t *testing.T) {
		t.Parallel()
		tok := mintToken(t, key, "alice", issuer, time.Now().Add(-time.Minute))
		if _, err := a.Authenticate(reqWithToken(tok)); err == nil {
			t.Fatal("expected expired token to fail")
		}
	})

	t.Run("wrong key", func(t *testing.T) {
		t.Parallel()
		tok := mintToken(t, other, "alice", issuer, time.Now().Add(time.Hour))
		if _, err := a.Authenticate(reqWithToken(tok)); err == nil {
			t.Fatal("expected signature mismatch to fail")
		}
	})

	t.Run("missing", func(t *testing.T) {
		t.Parallel()
		if _, err := a.Authenticate(httptest.NewRequest(http.MethodGet, "/", nil)); err == nil {
			t.Fatal("expected missing token to fail")
		}
	})
}

func TestParseECPublicKeyPEM(t *testing.T) {
	t.Parallel()

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	good := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})

	if _, err := auth.ParseECPublicKeyPEM(good); err != nil {
		t.Fatalf("parse good key: %v", err)
	}
	if _, err := auth.ParseECPublicKeyPEM([]byte("not pem")); err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func reqWithToken(token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/vpc", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}
