// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package handler_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/crypto"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/handler"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/ssl"
	"github.com/goabonga/infrastructure/internal/state"
)

func newSSLMux(t *testing.T) *http.ServeMux {
	t.Helper()
	key, _ := crypto.GenerateKey()
	kek, err := crypto.NewKEK(key)
	if err != nil {
		t.Fatalf("kek: %v", err)
	}
	store := state.NewFileStore(t.TempDir())
	reg := registry.New[resource.SSLCASpec, resource.SSLCAStatus](store, resource.KindSSLCA)
	certs := registry.New[resource.SSLCertSpec, resource.SSLCertStatus](store, resource.KindSSLCert)
	mux := http.NewServeMux()
	handler.NewSSLHandler(ssl.NewService(reg, certs, kek)).Register(mux, "/api/v1")
	return mux
}

func TestSSLHandlerCreateAndIssue(t *testing.T) {
	t.Parallel()

	mux := newSSLMux(t)

	// Create CA.
	body := resource.SSLCA{Spec: resource.SSLCASpec{CommonName: "infra root"}}
	rec := do(t, mux, http.MethodPut, "/api/v1/ssl_ca/ca-1", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("put CA status = %d, body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "certPem") {
		t.Fatalf("CA response missing cert: %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), "encryptedKey") {
		t.Fatalf("CA response leaks key: %s", rec.Body)
	}

	// Issue a leaf certificate.
	rec = do(t, mux, http.MethodPost, "/api/v1/ssl_ca/ca-1/issue", issueBody{CommonName: "web", DNSNames: []string{"example.com"}, ValidDays: 30})
	if rec.Code != http.StatusOK {
		t.Fatalf("issue status = %d, body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "BEGIN CERTIFICATE") || !strings.Contains(rec.Body.String(), "PRIVATE KEY") {
		t.Fatalf("issue response missing cert/key: %s", rec.Body)
	}

	// Delete then 404.
	if rec := do(t, mux, http.MethodDelete, "/api/v1/ssl_ca/ca-1", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
	if rec := do(t, mux, http.MethodGet, "/api/v1/ssl_ca/ca-1", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get-after-delete status = %d", rec.Code)
	}
}

func TestSSLHandlerValidation(t *testing.T) {
	t.Parallel()

	mux := newSSLMux(t)

	if rec := do(t, mux, http.MethodPut, "/api/v1/ssl_ca/ca-1", resource.SSLCA{Spec: resource.SSLCASpec{}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty CN put status = %d, want 400", rec.Code)
	}
	if rec := do(t, mux, http.MethodPost, "/api/v1/ssl_ca/ghost/issue", issueBody{CommonName: "web"}); rec.Code != http.StatusNotFound {
		t.Fatalf("issue against missing CA status = %d, want 404", rec.Code)
	}
	if rec := do(t, mux, http.MethodPost, "/api/v1/ssl_ca/ghost/issue", issueBody{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("issue empty CN status = %d, want 400", rec.Code)
	}
}

type issueBody struct {
	CommonName string   `json:"commonName"`
	DNSNames   []string `json:"dnsNames,omitempty"`
	ValidDays  int      `json:"validDays,omitempty"`
}

func TestSSLHandlerLeavesTheGlobalRootToThePlatform(t *testing.T) {
	t.Parallel()

	mux := newSSLMux(t)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, "/api/v1/ssl_ca/mine", resource.SSLCA{Spec: resource.SSLCASpec{CommonName: "Mine", Global: true}}},
		{http.MethodPut, "/api/v1/ssl_ca/public-root", resource.SSLCA{Spec: resource.SSLCASpec{CommonName: "Imposter"}}},
		{http.MethodDelete, "/api/v1/ssl_ca/public-root", nil},
	} {
		if rec := do(t, mux, tc.method, tc.path, tc.body); rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s: status %d, want 403", tc.method, tc.path, rec.Code)
		}
	}
}
