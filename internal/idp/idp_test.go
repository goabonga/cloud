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

const testConsoleURL = "http://console.example"

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	key, _ := idp.GenerateKey()
	srv := idp.NewServer(
		idp.NewIssuer(key, "http://idp", time.Hour),
		map[string]string{"svc": "s3cret"},
		newIdentityService(t),
		&key.PublicKey,
		"http://idp",
		testConsoleURL,
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
		testConsoleURL,
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
	if !strings.Contains(string(body), "device_authorization_endpoint") {
		t.Fatalf("discovery missing device_authorization_endpoint: %s", body)
	}
}

func loginAs(t *testing.T, ts *httptest.Server, username, password string) string {
	t.Helper()
	resp, err := http.Post(ts.URL+"/login", "application/json",
		strings.NewReader(`{"username":"`+username+`","password":"`+password+`"}`))
	if err != nil {
		t.Fatalf("login %s: %v", username, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s status = %d", username, resp.StatusCode)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return out.AccessToken
}

func startDeviceAuthorization(t *testing.T, ts *httptest.Server) (deviceCode, userCode string) {
	t.Helper()
	resp, err := http.Post(ts.URL+"/device_authorization", "application/x-www-form-urlencoded", nil)
	if err != nil {
		t.Fatalf("device_authorization: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("device_authorization status = %d", resp.StatusCode)
	}
	var out struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode device_authorization response: %v", err)
	}
	if out.DeviceCode == "" || out.UserCode == "" {
		t.Fatalf("empty codes in response: %+v", out)
	}
	if out.VerificationURI != testConsoleURL+"/device" {
		t.Fatalf("verification_uri = %q", out.VerificationURI)
	}
	if out.VerificationURIComplete != testConsoleURL+"/device?user_code="+url.QueryEscape(out.UserCode) {
		t.Fatalf("verification_uri_complete = %q", out.VerificationURIComplete)
	}
	if out.ExpiresIn <= 0 || out.Interval <= 0 {
		t.Fatalf("unexpected expires_in/interval: %+v", out)
	}
	return out.DeviceCode, out.UserCode
}

func pollDeviceToken(t *testing.T, ts *httptest.Server, deviceCode string) (status int, body map[string]string) {
	t.Helper()
	resp, err := http.PostForm(ts.URL+"/token", url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceCode},
	})
	if err != nil {
		t.Fatalf("poll token: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body = map[string]string{}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func TestDeviceAuthorizationHappyPath(t *testing.T) {
	t.Parallel()

	ts, users := newServerWithUsers(t)
	if _, err := users.Put("user-1", resource.UserSpec{Username: "alice", Password: "s3cr3t", Roles: []string{"admin"}}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	aliceToken := loginAs(t, ts, "alice", "s3cr3t")

	deviceCode, userCode := startDeviceAuthorization(t, ts)

	if status, body := pollDeviceToken(t, ts, deviceCode); status != http.StatusBadRequest || body["error"] != "authorization_pending" {
		t.Fatalf("poll before approval: status=%d body=%v", status, body)
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/device/verify", strings.NewReader(`{"user_code":"`+userCode+`","approve":true}`))
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	req.Header.Set("Content-Type", "application/json")
	verifyResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	defer func() { _ = verifyResp.Body.Close() }()
	if verifyResp.StatusCode != http.StatusNoContent {
		t.Fatalf("verify status = %d", verifyResp.StatusCode)
	}

	status, body := pollDeviceToken(t, ts, deviceCode)
	if status != http.StatusOK || body["access_token"] == "" {
		t.Fatalf("poll after approval: status=%d body=%v", status, body)
	}

	// The device_code is single-use.
	if status, body := pollDeviceToken(t, ts, deviceCode); status != http.StatusBadRequest || body["error"] != "expired_token" {
		t.Fatalf("poll after consumption: status=%d body=%v", status, body)
	}
}

func TestDeviceAuthorizationDenied(t *testing.T) {
	t.Parallel()

	ts, users := newServerWithUsers(t)
	if _, err := users.Put("user-1", resource.UserSpec{Username: "alice", Password: "s3cr3t"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	aliceToken := loginAs(t, ts, "alice", "s3cr3t")
	deviceCode, userCode := startDeviceAuthorization(t, ts)

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/device/verify", strings.NewReader(`{"user_code":"`+userCode+`","approve":false}`))
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	req.Header.Set("Content-Type", "application/json")
	verifyResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	defer func() { _ = verifyResp.Body.Close() }()
	if verifyResp.StatusCode != http.StatusNoContent {
		t.Fatalf("verify status = %d", verifyResp.StatusCode)
	}

	if status, body := pollDeviceToken(t, ts, deviceCode); status != http.StatusBadRequest || body["error"] != "access_denied" {
		t.Fatalf("poll after denial: status=%d body=%v", status, body)
	}
}

func TestDeviceAuthorizationUnknownCode(t *testing.T) {
	t.Parallel()

	ts := newServer(t)
	if status, body := pollDeviceToken(t, ts, "does-not-exist"); status != http.StatusBadRequest || body["error"] != "expired_token" {
		t.Fatalf("poll unknown device_code: status=%d body=%v", status, body)
	}
}

func TestDeviceVerifyRequiresAuthentication(t *testing.T) {
	t.Parallel()

	ts := newServer(t)
	_, userCode := startDeviceAuthorization(t, ts)

	resp, err := http.Post(ts.URL+"/device/verify", "application/json",
		strings.NewReader(`{"user_code":"`+userCode+`","approve":true}`))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated verify status = %d, want 401", resp.StatusCode)
	}
}

func TestDeviceVerifyUnknownUserCode(t *testing.T) {
	t.Parallel()

	ts, users := newServerWithUsers(t)
	if _, err := users.Put("user-1", resource.UserSpec{Username: "alice", Password: "s3cr3t"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	aliceToken := loginAs(t, ts, "alice", "s3cr3t")

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/device/verify", strings.NewReader(`{"user_code":"NOPE-NOPE","approve":true}`))
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("verify unknown user_code status = %d, want 404", resp.StatusCode)
	}
}
