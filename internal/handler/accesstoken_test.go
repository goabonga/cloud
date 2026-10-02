// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package handler_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/accesstoken"
	"github.com/goabonga/infrastructure/internal/auth"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/handler"
	"github.com/goabonga/infrastructure/internal/identity"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func newAccessTokenMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux, _ := newAccessTokenEnv(t)
	return mux
}

// newAccessTokenEnv is like newAccessTokenMux but also returns the raw
// store, so a test can seed a corrupt record to exercise an error path
// accesstoken.Service's own API can't reach.
func newAccessTokenEnv(t *testing.T) (mux *http.ServeMux, store *state.FileStore) {
	t.Helper()
	mux, store, _ = newAccessTokenEnvWithDir(t)
	return mux, store
}

// newAccessTokenEnvWithDir is like newAccessTokenEnv but also returns the
// backing directory, for a test that needs to tamper with on-disk
// permissions.
func newAccessTokenEnvWithDir(t *testing.T) (mux *http.ServeMux, store *state.FileStore, dir string) {
	t.Helper()
	dir = t.TempDir()
	store = state.NewFileStore(dir)
	users := identity.NewService(registry.New[resource.UserSpec, resource.UserStatus](store, resource.KindUser))
	reg := registry.New[resource.AccessTokenSpec, resource.AccessTokenStatus](store, resource.KindAccessToken)
	mux = http.NewServeMux()
	handler.NewAccessTokenHandler(accesstoken.NewService(reg, users)).Register(mux, "/api/v1")
	return mux, store, dir
}

func TestAccessTokenHandlerRequireAdminGatesEveryRoute(t *testing.T) {
	t.Parallel()

	mux := newAccessTokenMux(t)
	body := resource.AccessToken{Spec: resource.AccessTokenSpec{Name: "ci"}}

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/access_token"},
		{http.MethodGet, "/api/v1/access_token/t-1"},
		{http.MethodPut, "/api/v1/access_token/t-1"},
		{http.MethodDelete, "/api/v1/access_token/t-1"},
	} {
		if rec := do(t, mux, tc.method, tc.path, body); rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s unauthenticated status = %d, want 403", tc.method, tc.path, rec.Code)
		}
		if rec := doAs(t, mux, "bob", nil, tc.method, tc.path, body); rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s non-admin status = %d, want 403", tc.method, tc.path, rec.Code)
		}
	}
}

func TestAccessTokenHandlerLifecycle(t *testing.T) {
	t.Parallel()

	mux := newAccessTokenMux(t)
	admin := []string{handler.AdminRole}

	// Create returns the plaintext token exactly once.
	rec := doAs(t, mux, "root", admin, http.MethodPut, "/api/v1/access_token/t-1", resource.AccessToken{Spec: resource.AccessTokenSpec{Name: "ci"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"token":"infra_`) {
		t.Fatalf("create response missing plaintext token: %s", rec.Body)
	}

	// Get is redacted (no token hash, no plaintext).
	rec = doAs(t, mux, "root", admin, http.MethodGet, "/api/v1/access_token/t-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "infra_") || strings.Contains(rec.Body.String(), "tokenHash") {
		t.Fatalf("get leaks token material: %s", rec.Body)
	}

	// Update (same uid) never regenerates the token.
	rec = doAs(t, mux, "root", admin, http.MethodPut, "/api/v1/access_token/t-1", resource.AccessToken{Spec: resource.AccessTokenSpec{Name: "ci-renamed"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, body %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), `"token":"infra_`) {
		t.Fatalf("update response should not regenerate the token: %s", rec.Body)
	}

	// Decode error.
	req := httptest.NewRequest(http.MethodPut, "/api/v1/access_token/t-1", bytes.NewBufferString(`{"bogus": true}`))
	req = req.WithContext(auth.WithIdentity(req.Context(), &auth.Identity{Subject: "root", Roles: admin}))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid body status = %d, want 400", rr.Code)
	}

	// Validation error (empty name).
	if rec := doAs(t, mux, "root", admin, http.MethodPut, "/api/v1/access_token/t-2", resource.AccessToken{Spec: resource.AccessTokenSpec{}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty name status = %d, want 400", rec.Code)
	}

	// List.
	rec = doAs(t, mux, "root", admin, http.MethodGet, "/api/v1/access_token", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var list resource.List[resource.AccessTokenSpec, resource.AccessTokenStatus]
	mustDecode(t, rec, &list)
	if len(list.Items) != 1 {
		t.Fatalf("list items = %d, want 1", len(list.Items))
	}

	// Get missing -> 404.
	if rec := doAs(t, mux, "root", admin, http.MethodGet, "/api/v1/access_token/ghost", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing get status = %d, want 404", rec.Code)
	}

	// Delete.
	if rec := doAs(t, mux, "root", admin, http.MethodDelete, "/api/v1/access_token/t-1", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
}

func TestAccessTokenHandlerListPropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, store := newAccessTokenEnv(t)
	if err := store.Put(resource.KindAccessToken+"/garbled", []byte("not json")); err != nil {
		t.Fatalf("seed corrupt entry: %v", err)
	}

	rec := doAs(t, mux, "root", []string{handler.AdminRole}, http.MethodGet, "/api/v1/access_token", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("list status = %d, want 500", rec.Code)
	}
}

func TestAccessTokenHandlerGetPropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, store := newAccessTokenEnv(t)
	if err := store.Put(resource.KindAccessToken+"/corrupt", []byte("not json")); err != nil {
		t.Fatalf("seed corrupt entry: %v", err)
	}

	rec := doAs(t, mux, "root", []string{handler.AdminRole}, http.MethodGet, "/api/v1/access_token/corrupt", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("get status = %d, want 500", rec.Code)
	}
}

func TestAccessTokenHandlerDeletePropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, _, dir := newAccessTokenEnvWithDir(t)
	admin := []string{handler.AdminRole}

	if rec := doAs(t, mux, "root", admin, http.MethodPut, "/api/v1/access_token/t-1", resource.AccessToken{Spec: resource.AccessTokenSpec{Name: "ci"}}); rec.Code != http.StatusOK {
		t.Fatalf("seed create status = %d, body %s", rec.Code, rec.Body)
	}

	tokenDir := filepath.Join(dir, resource.KindAccessToken)
	if err := os.Chmod(tokenDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(tokenDir, 0o755) })

	rec := doAs(t, mux, "root", admin, http.MethodDelete, "/api/v1/access_token/t-1", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete status = %d, want 500", rec.Code)
	}
}
