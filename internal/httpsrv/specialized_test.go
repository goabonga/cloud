// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package httpsrv_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/auth"
	"github.com/goabonga/infrastructure/internal/crypto"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/httpsrv"
	"github.com/goabonga/infrastructure/internal/registry"
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
	if r.Header.Get("Authorization") == "Bearer root" {
		return &auth.Identity{Subject: "root", Roles: []string{"admin"}}, nil
	}
	return &auth.Identity{Subject: "bob"}, nil
}
