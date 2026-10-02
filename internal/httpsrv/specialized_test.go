// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package httpsrv_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/auth"
	"github.com/goabonga/infrastructure/internal/crypto"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/httpsrv"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/ssl"
	"github.com/goabonga/infrastructure/internal/state"
)

func TestSpecializedRoutesRejectUnprivilegedUsers(t *testing.T) {
	store := state.NewFileStore(t.TempDir())
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	kek, err := crypto.NewKEK(key)
	if err != nil {
		t.Fatal(err)
	}
	fn := &resource.Function{Metadata: resource.ObjectMeta{UID: "fn", OwnerUID: "alice"}}
	if err := registry.New[resource.FunctionSpec, resource.FunctionStatus](store, resource.KindFunction).Put(fn); err != nil {
		t.Fatal(err)
	}
	authn := specializedAuthenticator{}
	h := httpsrv.New(store, httpsrv.WithAuth(authn), httpsrv.WithSecretEncryption(kek)).Handler()
	for _, route := range []struct{ method, path string }{{"GET", "secret"}, {"GET", "secret/s/reveal"}, {"GET", "secret_version/s/reveal"}, {"POST", "ssl_ca/root/issue"}, {"GET", "ssl_cert/c/reveal"}, {"POST", "function/fn/invoke"}} {
		r := httptest.NewRequest(route.method, "/api/v1/"+route.path, strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer bob")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: %d", route.path, w.Code)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/secret", nil)
	r.Header.Set("Authorization", "Bearer root")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("admin: %d", w.Code)
	}
}

type specializedAuthenticator struct{}

func (specializedAuthenticator) Authenticate(r *http.Request) (*auth.Identity, error) {
	if r.Header.Get("Authorization") == "" {
		return nil, errors.New("missing token")
	}
	if r.Header.Get("Authorization") == "Bearer root" {
		return &auth.Identity{Subject: "root", Roles: []string{"admin"}}, nil
	}
	return &auth.Identity{Subject: "bob"}, nil
}

func TestPublicRootReadDoesNotExposePrivateOperations(t *testing.T) {
	store := state.NewFileStore(t.TempDir())
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	kek, err := crypto.NewKEK(key)
	if err != nil {
		t.Fatal(err)
	}
	cas := registry.New[resource.SSLCASpec, resource.SSLCAStatus](store, resource.KindSSLCA)
	certs := registry.New[resource.SSLCertSpec, resource.SSLCertStatus](store, resource.KindSSLCert)
	svc := ssl.NewService(cas, certs, kek)
	if _, err := svc.EnsureGlobalRoot("root", "infra"); err != nil {
		t.Fatal(err)
	}
	h := httpsrv.New(store, httpsrv.WithAuth(specializedAuthenticator{}), httpsrv.WithSecretEncryption(kek)).Handler()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/ssl_ca/public-root", nil)
	r.Header.Set("Authorization", "Bearer bob")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("public root read: %d %s", w.Code, w.Body)
	}
	var ca resource.SSLCA
	if err := json.NewDecoder(w.Body).Decode(&ca); err != nil {
		t.Fatal(err)
	}
	if len(ca.Status.CertPEM) == 0 || len(ca.Status.EncryptedKey) != 0 {
		t.Fatal("certificate missing or private key exposed")
	}
	for _, route := range []struct{ method, path string }{
		{"POST", "ssl_ca/public-root/issue"}, {"PUT", "ssl_ca/public-root"}, {"DELETE", "ssl_ca/public-root"},
		{"GET", "ssl_ca"}, {"GET", "ssl_ca/private"}, {"GET", "ssl_ca/public-root/reveal"},
	} {
		req := httptest.NewRequest(route.method, "/api/v1/"+route.path, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer bob")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, req)
		if response.Code != http.StatusForbidden {
			t.Errorf("%s %s: %d", route.method, route.path, response.Code)
		}
	}
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/api/v1/ssl_ca/public-root", nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated root read: %d", unauth.Code)
	}
}
