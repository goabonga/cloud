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

	"github.com/goabonga/infrastructure/internal/crypto"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/handler"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/secret"
	"github.com/goabonga/infrastructure/internal/state"
)

func newSecretMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux, _, _, _ := newSecretEnv(t)
	return mux
}

// newSecretEnv is like newSecretMux but also returns the registries and the
// raw store, so a test can seed a corrupt or hand-crafted record to exercise
// an error path the service's own API can't reach.
func newSecretEnv(t *testing.T) (mux *http.ServeMux, reg *registry.Registry[resource.SecretSpec, resource.SecretStatus], versions *registry.Registry[resource.SecretVersionSpec, resource.SecretVersionStatus], store *state.FileStore) {
	t.Helper()
	mux, reg, versions, store, _ = newSecretEnvWithDir(t)
	return mux, reg, versions, store
}

// newSecretEnvWithDir is like newSecretEnv but also returns the backing
// directory, for a test that needs to tamper with on-disk permissions.
func newSecretEnvWithDir(t *testing.T) (mux *http.ServeMux, reg *registry.Registry[resource.SecretSpec, resource.SecretStatus], versions *registry.Registry[resource.SecretVersionSpec, resource.SecretVersionStatus], store *state.FileStore, dir string) {
	t.Helper()
	key, _ := crypto.GenerateKey()
	kek, err := crypto.NewKEK(key)
	if err != nil {
		t.Fatalf("kek: %v", err)
	}
	dir = t.TempDir()
	store = state.NewFileStore(dir)
	reg = registry.New[resource.SecretSpec, resource.SecretStatus](store, resource.KindSecret)
	versions = registry.New[resource.SecretVersionSpec, resource.SecretVersionStatus](store, resource.KindSecretVersion)
	mux = http.NewServeMux()
	handler.NewSecretHandler(secret.NewService(reg, versions, kek)).Register(mux, "/api/v1")
	return mux, reg, versions, store, dir
}

func TestSecretHandlerLifecycle(t *testing.T) {
	t.Parallel()

	mux := newSecretMux(t)

	// Create.
	body := resource.Secret{Metadata: resource.ObjectMeta{Name: "db"}, Spec: resource.SecretSpec{Data: "s3cr3t"}}
	rec := do(t, mux, http.MethodPut, "/api/v1/secret/sec-1", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("put status = %d, body %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "s3cr3t") || strings.Contains(rec.Body.String(), "ciphertext") {
		t.Fatalf("put response leaks secret material: %s", rec.Body)
	}

	// Get is redacted.
	rec = do(t, mux, http.MethodGet, "/api/v1/secret/sec-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Fatalf("get leaks plaintext: %s", rec.Body)
	}

	// Reveal returns plaintext.
	rec = do(t, mux, http.MethodGet, "/api/v1/secret/sec-1/reveal", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reveal status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Fatalf("reveal missing plaintext: %s", rec.Body)
	}

	// Delete then 404.
	rec = do(t, mux, http.MethodDelete, "/api/v1/secret/sec-1", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
	rec = do(t, mux, http.MethodGet, "/api/v1/secret/sec-1", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get-after-delete status = %d", rec.Code)
	}
}

func TestSecretHandlerListRedacts(t *testing.T) {
	t.Parallel()

	mux := newSecretMux(t)
	for _, uid := range []string{"sec-1", "sec-2"} {
		body := resource.Secret{Spec: resource.SecretSpec{Data: "plaintext-" + uid}}
		if rec := do(t, mux, http.MethodPut, "/api/v1/secret/"+uid, body); rec.Code != http.StatusOK {
			t.Fatalf("put %s: %d", uid, rec.Code)
		}
	}

	rec := do(t, mux, http.MethodGet, "/api/v1/secret", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "plaintext-") || strings.Contains(rec.Body.String(), "ciphertext") {
		t.Fatalf("list leaks secret material: %s", rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "sec-1") || !strings.Contains(rec.Body.String(), "sec-2") {
		t.Fatalf("list missing entries: %s", rec.Body)
	}
}

func TestSecretHandlerRejectsEmptyData(t *testing.T) {
	t.Parallel()

	mux := newSecretMux(t)
	rec := do(t, mux, http.MethodPut, "/api/v1/secret/sec-1", resource.Secret{Spec: resource.SecretSpec{}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty-data status = %d, want 400", rec.Code)
	}
}

func TestSecretHandlerRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	mux := newSecretMux(t)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/secret/sec-1", bytes.NewBufferString(`{"bogus": true}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field status = %d, want 400", rec.Code)
	}
}

func TestSecretHandlerListPropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, _, _, store := newSecretEnv(t)
	if err := store.Put(resource.KindSecret+"/garbled", []byte("not json")); err != nil {
		t.Fatalf("seed corrupt entry: %v", err)
	}

	rec := do(t, mux, http.MethodGet, "/api/v1/secret", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("list status = %d, want 500", rec.Code)
	}
}

func TestSecretHandlerGetPropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, _, _, store := newSecretEnv(t)
	if err := store.Put(resource.KindSecret+"/sec-1", []byte("not json")); err != nil {
		t.Fatalf("seed corrupt entry: %v", err)
	}

	rec := do(t, mux, http.MethodGet, "/api/v1/secret/sec-1", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("get status = %d, want 500", rec.Code)
	}
}

func TestSecretHandlerRevealMissingIsNotFound(t *testing.T) {
	t.Parallel()

	mux := newSecretMux(t)
	rec := do(t, mux, http.MethodGet, "/api/v1/secret/ghost/reveal", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("reveal status = %d, want 404", rec.Code)
	}
}

func TestSecretHandlerRevealPropagatesDecryptError(t *testing.T) {
	t.Parallel()

	mux, reg, _, _ := newSecretEnv(t)
	// A record whose ciphertext isn't even a well-formed envelope: Reveal must
	// surface that as a server error, not treat it as "not found".
	if err := reg.Put(&resource.Secret{
		Metadata: resource.ObjectMeta{UID: "sec-1"},
		Status:   resource.SecretStatus{Ciphertext: []byte("not an envelope")},
	}); err != nil {
		t.Fatalf("seed corrupt secret: %v", err)
	}

	rec := do(t, mux, http.MethodGet, "/api/v1/secret/sec-1/reveal", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("reveal status = %d, want 500", rec.Code)
	}
}

func TestSecretHandlerPutPropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, _, _, _, dir := newSecretEnvWithDir(t)
	secretDir := filepath.Join(dir, resource.KindSecret)
	if err := os.MkdirAll(secretDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(secretDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(secretDir, 0o755) })

	rec := do(t, mux, http.MethodPut, "/api/v1/secret/sec-1", resource.Secret{Spec: resource.SecretSpec{Data: "s3cr3t"}})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("put status = %d, want 500", rec.Code)
	}
}

func TestSecretHandlerDeletePropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, _, _, _, dir := newSecretEnvWithDir(t)
	if rec := do(t, mux, http.MethodPut, "/api/v1/secret/sec-1", resource.Secret{Spec: resource.SecretSpec{Data: "s3cr3t"}}); rec.Code != http.StatusOK {
		t.Fatalf("seed create status = %d", rec.Code)
	}

	secretDir := filepath.Join(dir, resource.KindSecret)
	if err := os.Chmod(secretDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(secretDir, 0o755) })

	rec := do(t, mux, http.MethodDelete, "/api/v1/secret/sec-1", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete status = %d, want 500", rec.Code)
	}
}

func TestSecretVersionHandlerLifecycle(t *testing.T) {
	t.Parallel()

	mux := newSecretMux(t)

	// Seed the parent secret first (versions don't strictly require it to
	// exist, but this mirrors how the API is actually used).
	if rec := do(t, mux, http.MethodPut, "/api/v1/secret/sec-1", resource.Secret{Spec: resource.SecretSpec{Data: "v0"}}); rec.Code != http.StatusOK {
		t.Fatalf("seed secret status = %d", rec.Code)
	}

	// Create a version.
	body := resource.SecretVersion{Spec: resource.SecretVersionSpec{SecretID: "sec-1", Data: "v1-data"}}
	rec := do(t, mux, http.MethodPut, "/api/v1/secret_version/ver-1", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("put version status = %d, body %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "v1-data") || strings.Contains(rec.Body.String(), "ciphertext") {
		t.Fatalf("put version response leaks data: %s", rec.Body)
	}
	var created resource.SecretVersion
	mustDecode(t, rec, &created)
	if created.Status.Version != 1 {
		t.Fatalf("version = %d, want 1", created.Status.Version)
	}

	// A second version for the same secret sequences to 2.
	rec = do(t, mux, http.MethodPut, "/api/v1/secret_version/ver-2", resource.SecretVersion{Spec: resource.SecretVersionSpec{SecretID: "sec-1", Data: "v2-data"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("put second version status = %d", rec.Code)
	}
	var created2 resource.SecretVersion
	mustDecode(t, rec, &created2)
	if created2.Status.Version != 2 {
		t.Fatalf("second version = %d, want 2", created2.Status.Version)
	}

	// Get a version.
	rec = do(t, mux, http.MethodGet, "/api/v1/secret_version/ver-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get version status = %d", rec.Code)
	}

	// Reveal a version.
	rec = do(t, mux, http.MethodGet, "/api/v1/secret_version/ver-1/reveal", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reveal version status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "v1-data") {
		t.Fatalf("reveal version missing plaintext: %s", rec.Body)
	}

	// List versions, filtered by secretId.
	rec = do(t, mux, http.MethodGet, "/api/v1/secret_version?secretId=sec-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list versions status = %d", rec.Code)
	}
	var list resource.List[resource.SecretVersionSpec, resource.SecretVersionStatus]
	mustDecode(t, rec, &list)
	if len(list.Items) != 2 {
		t.Fatalf("list versions = %d items, want 2", len(list.Items))
	}

	// Delete a version then 404 on get.
	rec = do(t, mux, http.MethodDelete, "/api/v1/secret_version/ver-1", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete version status = %d", rec.Code)
	}
	rec = do(t, mux, http.MethodGet, "/api/v1/secret_version/ver-1", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get-after-delete version status = %d", rec.Code)
	}
}

func TestSecretVersionHandlerRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	mux := newSecretMux(t)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/secret_version/ver-1", bytes.NewBufferString(`{"bogus": true}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field status = %d, want 400", rec.Code)
	}
}

func TestSecretVersionHandlerRejectsInvalidSpec(t *testing.T) {
	t.Parallel()

	mux := newSecretMux(t)
	for _, tc := range []resource.SecretVersionSpec{
		{SecretID: "", Data: "v1"},
		{SecretID: "sec-1", Data: ""},
	} {
		rec := do(t, mux, http.MethodPut, "/api/v1/secret_version/ver-1", resource.SecretVersion{Spec: tc})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("spec %+v status = %d, want 400", tc, rec.Code)
		}
	}
}

func TestSecretVersionHandlerGetAndRevealMissingAreNotFound(t *testing.T) {
	t.Parallel()

	mux := newSecretMux(t)
	if rec := do(t, mux, http.MethodGet, "/api/v1/secret_version/ghost", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("get status = %d, want 404", rec.Code)
	}
	if rec := do(t, mux, http.MethodGet, "/api/v1/secret_version/ghost/reveal", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("reveal status = %d, want 404", rec.Code)
	}
}

func TestSecretVersionHandlerListPropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, _, _, store := newSecretEnv(t)
	if err := store.Put(resource.KindSecretVersion+"/garbled", []byte("not json")); err != nil {
		t.Fatalf("seed corrupt entry: %v", err)
	}

	rec := do(t, mux, http.MethodGet, "/api/v1/secret_version", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("list versions status = %d, want 500", rec.Code)
	}
}

func TestSecretVersionHandlerGetPropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, _, _, store := newSecretEnv(t)
	if err := store.Put(resource.KindSecretVersion+"/ver-1", []byte("not json")); err != nil {
		t.Fatalf("seed corrupt entry: %v", err)
	}

	rec := do(t, mux, http.MethodGet, "/api/v1/secret_version/ver-1", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("get version status = %d, want 500", rec.Code)
	}
}

func TestSecretVersionHandlerRevealPropagatesDecryptError(t *testing.T) {
	t.Parallel()

	mux, _, versions, _ := newSecretEnv(t)
	if err := versions.Put(&resource.SecretVersion{
		Metadata: resource.ObjectMeta{UID: "ver-1"},
		Spec:     resource.SecretVersionSpec{SecretID: "sec-1"},
		Status:   resource.SecretVersionStatus{Ciphertext: []byte("not an envelope")},
	}); err != nil {
		t.Fatalf("seed corrupt version: %v", err)
	}

	rec := do(t, mux, http.MethodGet, "/api/v1/secret_version/ver-1/reveal", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("reveal version status = %d, want 500", rec.Code)
	}
}

func TestSecretVersionHandlerPutPropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, _, _, _, dir := newSecretEnvWithDir(t)
	versionDir := filepath.Join(dir, resource.KindSecretVersion)
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(versionDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(versionDir, 0o755) })

	rec := do(t, mux, http.MethodPut, "/api/v1/secret_version/ver-1", resource.SecretVersion{Spec: resource.SecretVersionSpec{SecretID: "sec-1", Data: "v1"}})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("put version status = %d, want 500", rec.Code)
	}
}

func TestSecretVersionHandlerDeletePropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, _, _, _, dir := newSecretEnvWithDir(t)
	if rec := do(t, mux, http.MethodPut, "/api/v1/secret_version/ver-1", resource.SecretVersion{Spec: resource.SecretVersionSpec{SecretID: "sec-1", Data: "v1"}}); rec.Code != http.StatusOK {
		t.Fatalf("seed create status = %d", rec.Code)
	}

	versionDir := filepath.Join(dir, resource.KindSecretVersion)
	if err := os.Chmod(versionDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(versionDir, 0o755) })

	rec := do(t, mux, http.MethodDelete, "/api/v1/secret_version/ver-1", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete version status = %d, want 500", rec.Code)
	}
}
