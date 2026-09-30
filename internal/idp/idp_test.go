// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package idp_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/goabonga/infrastructure/internal/idp"
)

func TestKeyPEMRoundtrip(t *testing.T) {
	t.Parallel()

	key, err := idp.GenerateKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	privPEM, err := idp.MarshalPrivateKeyPEM(key)
	if err != nil {
		t.Fatalf("marshal priv: %v", err)
	}
	loaded, err := idp.ParsePrivateKeyPEM(privPEM)
	if err != nil {
		t.Fatalf("parse priv: %v", err)
	}
	if !loaded.Equal(key) {
		t.Fatal("round-tripped key differs")
	}
	if _, err := idp.MarshalPublicKeyPEM(&key.PublicKey); err != nil {
		t.Fatalf("marshal pub: %v", err)
	}
}

func TestPublicJWKSEncodesKeyCoordinates(t *testing.T) {
	t.Parallel()

	key, err := idp.GenerateKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	ks, err := idp.PublicJWKS(&key.PublicKey, "kid")
	if err != nil {
		t.Fatalf("jwks: %v", err)
	}
	if len(ks.Keys) != 1 {
		t.Fatalf("expected one key, got %d", len(ks.Keys))
	}
	x, err := base64.RawURLEncoding.DecodeString(ks.Keys[0].X)
	if err != nil {
		t.Fatalf("decode x: %v", err)
	}
	y, err := base64.RawURLEncoding.DecodeString(ks.Keys[0].Y)
	if err != nil {
		t.Fatalf("decode y: %v", err)
	}
	// Uncompressed SEC 1 point: 0x04 || X || Y.
	point := append(append([]byte{4}, x...), y...)
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
	if err != nil {
		t.Fatalf("parse jwk point: %v", err)
	}
	if !pub.Equal(&key.PublicKey) {
		t.Fatal("jwk coordinates do not match the signing key")
	}
}

func TestIssueProducesVerifiableToken(t *testing.T) {
	t.Parallel()

	key, _ := idp.GenerateKey()
	issuer := idp.NewIssuer(key, "http://idp", time.Hour)
	token, err := issuer.Issue("alice")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	claims := &jwt.RegisteredClaims{}
	if _, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
		return &key.PublicKey, nil
	}, jwt.WithValidMethods([]string{"ES256"})); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.Subject != "alice" || claims.Issuer != "http://idp" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	key, _ := idp.GenerateKey()
	srv := idp.NewServer(
		idp.NewIssuer(key, "http://idp", time.Hour),
		map[string]string{"svc": "s3cret"},
		&key.PublicKey,
		"http://idp",
	)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestServerTokenEndpoint(t *testing.T) {
	t.Parallel()

	ts := newServer(t)

	// Valid client credentials.
	resp, err := http.PostForm(ts.URL+"/token", url.Values{"client_id": {"svc"}, "client_secret": {"s3cret"}})
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token status = %d", resp.StatusCode)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.AccessToken == "" || out.TokenType != "Bearer" {
		t.Fatalf("unexpected token response: %+v", out)
	}

	// Wrong secret.
	bad, err := http.PostForm(ts.URL+"/token", url.Values{"client_id": {"svc"}, "client_secret": {"nope"}})
	if err != nil {
		t.Fatalf("token bad: %v", err)
	}
	defer func() { _ = bad.Body.Close() }()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad secret status = %d, want 401", bad.StatusCode)
	}
}

func TestServerJWKSAndDiscovery(t *testing.T) {
	t.Parallel()

	ts := newServer(t)

	jwks, err := http.Get(ts.URL + "/jwks.json")
	if err != nil {
		t.Fatalf("jwks: %v", err)
	}
	defer func() { _ = jwks.Body.Close() }()
	var ks idp.JWKS
	if err := json.NewDecoder(jwks.Body).Decode(&ks); err != nil {
		t.Fatalf("decode jwks: %v", err)
	}
	if len(ks.Keys) != 1 {
		t.Fatalf("expected one key, got %d", len(ks.Keys))
	}

	disco, err := http.Get(ts.URL + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatalf("discovery: %v", err)
	}
	defer func() { _ = disco.Body.Close() }()
	body, _ := io.ReadAll(disco.Body)
	if !strings.Contains(string(body), "token_endpoint") {
		t.Fatalf("discovery missing token_endpoint: %s", body)
	}
}
