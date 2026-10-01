// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package httpsrv_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goabonga/infrastructure/internal/auth"
	"github.com/goabonga/infrastructure/internal/crypto"
	"github.com/goabonga/infrastructure/internal/httpsrv"
	"github.com/goabonga/infrastructure/internal/state"
)

func TestServerHealthz(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(httpsrv.New(state.NewFileStore(t.TempDir())).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("get healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d", resp.StatusCode)
	}
}

func TestServerWiresVPCRoutes(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(httpsrv.New(state.NewFileStore(t.TempDir())).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/vpc")
	if err != nil {
		t.Fatalf("get vpc collection: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("vpc list status = %d", resp.StatusCode)
	}
}

func TestServerAuthGatesAPIButNotHealth(t *testing.T) {
	t.Parallel()

	tokenAuth := auth.NewTokenAuthenticator(map[string]string{"tok": "alice"})
	srv := httptest.NewServer(httpsrv.New(state.NewFileStore(t.TempDir()), httpsrv.WithAuth(tokenAuth)).Handler())
	defer srv.Close()

	// Health is open.
	health, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	_ = health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d", health.StatusCode)
	}

	// API without a token is rejected.
	unauth, err := http.Get(srv.URL + "/api/v1/vpc")
	if err != nil {
		t.Fatalf("vpc no-auth: %v", err)
	}
	_ = unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-auth status = %d, want 401", unauth.StatusCode)
	}

	// API with the token succeeds.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/vpc", nil)
	req.Header.Set("Authorization", "Bearer tok")
	authed, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("vpc auth: %v", err)
	}
	_ = authed.Body.Close()
	if authed.StatusCode != http.StatusOK {
		t.Fatalf("auth status = %d, want 200", authed.StatusCode)
	}
}

func TestServerWiresACLRoutes(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(httpsrv.New(state.NewFileStore(t.TempDir())).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/acl_policy")
	if err != nil {
		t.Fatalf("get acl_policy collection: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("acl_policy list status = %d", resp.StatusCode)
	}
}

func TestServerWiresListenerRoutes(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(httpsrv.New(state.NewFileStore(t.TempDir())).Handler())
	defer srv.Close()

	for _, kind := range []string{"lb_target_group", "lb_target", "lb_listener"} {
		resp, err := http.Get(srv.URL + "/api/v1/" + kind)
		if err != nil {
			t.Fatalf("get %s collection: %v", kind, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s list status = %d", kind, resp.StatusCode)
		}
	}
}

func TestServerSecretRoutesDisabledByDefault(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(httpsrv.New(state.NewFileStore(t.TempDir())).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/secret")
	if err != nil {
		t.Fatalf("get secret collection: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("secret route should be absent without a KEK, got %d", resp.StatusCode)
	}
}

func TestServerSecretRoutesEnabledWithKEK(t *testing.T) {
	t.Parallel()

	key, _ := crypto.GenerateKey()
	kek, err := crypto.NewKEK(key)
	if err != nil {
		t.Fatalf("kek: %v", err)
	}
	srv := httptest.NewServer(httpsrv.New(state.NewFileStore(t.TempDir()), httpsrv.WithSecretEncryption(kek)).Handler())
	defer srv.Close()

	put, err := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/secret/sec-1",
		bytes.NewBufferString(`{"spec":{"data":"top-secret"}}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatalf("put secret: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put secret status = %d", resp.StatusCode)
	}

	// The SSL CA routes are wired by the same KEK option.
	ca, err := http.Get(srv.URL + "/api/v1/ssl_ca")
	if err != nil {
		t.Fatalf("get ssl_ca: %v", err)
	}
	defer func() { _ = ca.Body.Close() }()
	if ca.StatusCode != http.StatusOK {
		t.Fatalf("ssl_ca list status = %d", ca.StatusCode)
	}
}
