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

	"github.com/goabonga/infrastructure/internal/auth"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/handler"
	"github.com/goabonga/infrastructure/internal/identity"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func newUserMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux, _ := newUserEnv(t)
	return mux
}

// newUserEnv is like newUserMux but also returns the raw store, so a test can
// seed a corrupt record to exercise an error path identity.Service's own API
// can't reach.
func newUserEnv(t *testing.T) (mux *http.ServeMux, store *state.FileStore) {
	t.Helper()
	mux, store, _ = newUserEnvWithDir(t)
	return mux, store
}

// newUserEnvWithDir is like newUserEnv but also returns the backing
// directory, for a test that needs to tamper with on-disk permissions.
func newUserEnvWithDir(t *testing.T) (mux *http.ServeMux, store *state.FileStore, dir string) {
	t.Helper()
	dir = t.TempDir()
	store = state.NewFileStore(dir)
	reg := registry.New[resource.UserSpec, resource.UserStatus](store, resource.KindUser)
	mux = http.NewServeMux()
	handler.NewUserHandler(identity.NewService(reg)).Register(mux, "/api/v1")
	return mux, store, dir
}

func TestUserHandlerRequireAdminGatesListPutDelete(t *testing.T) {
	t.Parallel()

	mux := newUserMux(t)
	body := resource.User{Spec: resource.UserSpec{Username: "bob", Password: "s3cr3tpw"}}

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/user"},
		{http.MethodPut, "/api/v1/user/u-1"},
		{http.MethodDelete, "/api/v1/user/u-1"},
	} {
		if rec := do(t, mux, tc.method, tc.path, body); rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s unauthenticated status = %d, want 403", tc.method, tc.path, rec.Code)
		}
		if rec := doAs(t, mux, "bob", nil, tc.method, tc.path, body); rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s non-admin status = %d, want 403", tc.method, tc.path, rec.Code)
		}
	}
}

func TestUserHandlerLifecycle(t *testing.T) {
	t.Parallel()

	mux := newUserMux(t)
	admin := []string{handler.AdminRole}

	// Create (admin).
	rec := doAs(t, mux, "root", admin, http.MethodPut, "/api/v1/user/alice", resource.User{Spec: resource.UserSpec{Username: "alice", Password: "s3cr3tpw"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "s3cr3tpw") || strings.Contains(rec.Body.String(), "passwordHash") {
		t.Fatalf("create response leaks password material: %s", rec.Body)
	}

	// Self GET needs no admin role.
	rec = doAs(t, mux, "alice", nil, http.MethodGet, "/api/v1/user/alice", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("self get status = %d", rec.Code)
	}

	// Admin GET of someone else.
	rec = doAs(t, mux, "root", admin, http.MethodGet, "/api/v1/user/alice", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin get status = %d", rec.Code)
	}

	// Neither self nor admin: forbidden.
	if rec := doAs(t, mux, "bob", nil, http.MethodGet, "/api/v1/user/alice", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("other user get status = %d, want 403", rec.Code)
	}

	// Unauthenticated GET is also forbidden, not merely "not self".
	if rec := do(t, mux, http.MethodGet, "/api/v1/user/alice", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated get status = %d, want 403", rec.Code)
	}

	// Missing user, as admin -> 404.
	if rec := doAs(t, mux, "root", admin, http.MethodGet, "/api/v1/user/ghost", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing get status = %d, want 404", rec.Code)
	}

	// List as admin.
	rec = doAs(t, mux, "root", admin, http.MethodGet, "/api/v1/user", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var list resource.List[resource.UserSpec, resource.UserStatus]
	mustDecode(t, rec, &list)
	if len(list.Items) != 1 {
		t.Fatalf("list items = %d, want 1", len(list.Items))
	}

	// Update, decode error.
	req := httptest.NewRequest(http.MethodPut, "/api/v1/user/alice", bytes.NewBufferString(`{"bogus": true}`))
	req = req.WithContext(auth.WithIdentity(req.Context(), &auth.Identity{Subject: "root", Roles: admin}))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid body status = %d, want 400", rr.Code)
	}

	// Update, validation error (empty username).
	if rec := doAs(t, mux, "root", admin, http.MethodPut, "/api/v1/user/alice", resource.User{Spec: resource.UserSpec{Username: ""}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty username status = %d, want 400", rec.Code)
	}

	// Update (admin), does not require a password.
	rec = doAs(t, mux, "root", admin, http.MethodPut, "/api/v1/user/alice", resource.User{Spec: resource.UserSpec{Username: "alice", Roles: []string{"viewer"}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, body %s", rec.Code, rec.Body)
	}

	// Delete (admin).
	if rec := doAs(t, mux, "root", admin, http.MethodDelete, "/api/v1/user/alice", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
}

func TestUserHandlerListPropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, store := newUserEnv(t)
	if err := store.Put(resource.KindUser+"/garbled", []byte("not json")); err != nil {
		t.Fatalf("seed corrupt entry: %v", err)
	}

	rec := doAs(t, mux, "root", []string{handler.AdminRole}, http.MethodGet, "/api/v1/user", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("list status = %d, want 500", rec.Code)
	}
}

func TestUserHandlerGetPropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, store := newUserEnv(t)
	if err := store.Put(resource.KindUser+"/corrupt", []byte("not json")); err != nil {
		t.Fatalf("seed corrupt entry: %v", err)
	}

	rec := doAs(t, mux, "root", []string{handler.AdminRole}, http.MethodGet, "/api/v1/user/corrupt", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("get status = %d, want 500", rec.Code)
	}
}

func TestUserHandlerDeletePropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, _, dir := newUserEnvWithDir(t)
	admin := []string{handler.AdminRole}

	if rec := doAs(t, mux, "root", admin, http.MethodPut, "/api/v1/user/alice", resource.User{Spec: resource.UserSpec{Username: "alice", Password: "s3cr3tpw"}}); rec.Code != http.StatusOK {
		t.Fatalf("seed create status = %d, body %s", rec.Code, rec.Body)
	}

	userDir := filepath.Join(dir, resource.KindUser)
	if err := os.Chmod(userDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(userDir, 0o755) })

	rec := doAs(t, mux, "root", admin, http.MethodDelete, "/api/v1/user/alice", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete status = %d, want 500", rec.Code)
	}
}
