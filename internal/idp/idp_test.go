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

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/identity"
	"github.com/goabonga/infrastructure/internal/idp"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func newIdentityService(t *testing.T) *identity.Service {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	return identity.NewService(registry.New[resource.UserSpec, resource.UserStatus](store, resource.KindUser))
}

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
	token, err := issuer.Issue("alice", []string{"admin"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	claims := &struct {
		jwt.RegisteredClaims
		Roles []string `json:"roles,omitempty"`
	}{}
	if _, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
		return &key.PublicKey, nil
	}, jwt.WithValidMethods([]string{"ES256"})); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.Subject != "alice" || claims.Issuer != "http://idp" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != "admin" {
		t.Fatalf("roles = %v, want [admin]", claims.Roles)
	}
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	key, _ := idp.GenerateKey()
	srv := idp.NewServer(
		idp.NewIssuer(key, "http://idp", time.Hour),
		map[string]string{"svc": "s3cret"},
		newIdentityService(t),
		&key.PublicKey,
		"http://idp",
	)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func newServerWithUsers(t *testing.T) (*httptest.Server, *identity.Service) {
	t.Helper()
	key, _ := idp.GenerateKey()
	users := newIdentityService(t)
	srv := idp.NewServer(
		idp.NewIssuer(key, "http://idp", time.Hour),
		map[string]string{"svc": "s3cret"},
		users,
		&key.PublicKey,
		"http://idp",
	)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, users
}

func TestServerLoginEndpoint(t *testing.T) {
	t.Parallel()

	ts, users := newServerWithUsers(t)
	if _, err := users.Put("user-1", resource.UserSpec{Username: "alice", Password: "s3cr3t", Roles: []string{"admin"}}); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	body := strings.NewReader(`{"username":"alice","password":"s3cr3t"}`)
	resp, err := http.Post(ts.URL+"/login", "application/json", body)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.AccessToken == "" {
		t.Fatal("expected an access token")
	}

	// Wrong password.
	bad, err := http.Post(ts.URL+"/login", "application/json", strings.NewReader(`{"username":"alice","password":"nope"}`))
	if err != nil {
		t.Fatalf("login bad: %v", err)
	}
	defer func() { _ = bad.Body.Close() }()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad password status = %d, want 401", bad.StatusCode)
	}

	// userinfo with the issued token.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+out.AccessToken)
	info, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("userinfo: %v", err)
	}
	defer func() { _ = info.Body.Close() }()
	if info.StatusCode != http.StatusOK {
		t.Fatalf("userinfo status = %d", info.StatusCode)
	}
	var who struct {
		Subject string   `json:"subject"`
		Roles   []string `json:"roles"`
	}
	if err := json.NewDecoder(info.Body).Decode(&who); err != nil {
		t.Fatalf("decode userinfo: %v", err)
	}
	if who.Subject != "user-1" || len(who.Roles) != 1 || who.Roles[0] != "admin" {
		t.Fatalf("unexpected userinfo: %+v", who)
	}

	// userinfo without a token is unauthorized.
	anon, err := http.Get(ts.URL + "/userinfo")
	if err != nil {
		t.Fatalf("anon userinfo: %v", err)
	}
	defer func() { _ = anon.Body.Close() }()
	if anon.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anon userinfo status = %d, want 401", anon.StatusCode)
	}
}

func TestServerUserEndpointsRequireAdmin(t *testing.T) {
	t.Parallel()

	ts, users := newServerWithUsers(t)
	if _, err := users.Put("user-1", resource.UserSpec{Username: "alice", Password: "s3cr3t", Roles: []string{"admin"}}); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if _, err := users.Put("user-2", resource.UserSpec{Username: "bob", Password: "s3cr3t"}); err != nil {
		t.Fatalf("seed bob: %v", err)
	}

	login := func(username, password string) string {
		resp, err := http.Post(ts.URL+"/login", "application/json",
			strings.NewReader(`{"username":"`+username+`","password":"`+password+`"}`))
		if err != nil {
			t.Fatalf("login %s: %v", username, err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out struct {
			AccessToken string `json:"access_token"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out.AccessToken
	}
	adminToken := login("alice", "s3cr3t")
	bobToken := login("bob", "s3cr3t")

	get := func(path, token string) int {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("get %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode
	}

	if got := get("/user", adminToken); got != http.StatusOK {
		t.Fatalf("admin list users = %d, want 200", got)
	}
	if got := get("/user", bobToken); got != http.StatusForbidden {
		t.Fatalf("non-admin list users = %d, want 403", got)
	}
	if got := get("/user/user-2", bobToken); got != http.StatusOK {
		t.Fatalf("bob reading own record = %d, want 200", got)
	}
	if got := get("/user/user-1", bobToken); got != http.StatusForbidden {
		t.Fatalf("bob reading alice's record = %d, want 403", got)
	}
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
