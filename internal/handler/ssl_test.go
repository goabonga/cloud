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
	"github.com/goabonga/infrastructure/internal/ssl"
	"github.com/goabonga/infrastructure/internal/state"
)

func newSSLMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux, _, _, _ := newSSLEnv(t)
	return mux
}

// newSSLEnv is like newSSLMux but also returns the registries and the raw
// store, so a test can seed a corrupt or hand-crafted record to exercise an
// error path the service's own API can't reach.
func newSSLEnv(t *testing.T) (mux *http.ServeMux, reg *registry.Registry[resource.SSLCASpec, resource.SSLCAStatus], certs *registry.Registry[resource.SSLCertSpec, resource.SSLCertStatus], store *state.FileStore) {
	t.Helper()
	mux, reg, certs, store, _ = newSSLEnvWithDir(t)
	return mux, reg, certs, store
}

// newSSLEnvWithDir is like newSSLEnv but also returns the backing directory,
// for a test that needs to tamper with on-disk permissions.
func newSSLEnvWithDir(t *testing.T) (mux *http.ServeMux, reg *registry.Registry[resource.SSLCASpec, resource.SSLCAStatus], certs *registry.Registry[resource.SSLCertSpec, resource.SSLCertStatus], store *state.FileStore, dir string) {
	t.Helper()
	key, _ := crypto.GenerateKey()
	kek, err := crypto.NewKEK(key)
	if err != nil {
		t.Fatalf("kek: %v", err)
	}
	dir = t.TempDir()
	store = state.NewFileStore(dir)
	reg = registry.New[resource.SSLCASpec, resource.SSLCAStatus](store, resource.KindSSLCA)
	certs = registry.New[resource.SSLCertSpec, resource.SSLCertStatus](store, resource.KindSSLCert)
	mux = http.NewServeMux()
	handler.NewSSLHandler(ssl.NewService(reg, certs, kek)).Register(mux, "/api/v1")
	return mux, reg, certs, store, dir
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

func TestSSLHandlerListAndGet(t *testing.T) {
	t.Parallel()

	mux := newSSLMux(t)

	rec := do(t, mux, http.MethodGet, "/api/v1/ssl_ca", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty list status = %d", rec.Code)
	}
	var empty resource.List[resource.SSLCASpec, resource.SSLCAStatus]
	mustDecode(t, rec, &empty)
	if len(empty.Items) != 0 {
		t.Fatalf("expected empty list, got %d", len(empty.Items))
	}

	if rec := do(t, mux, http.MethodPut, "/api/v1/ssl_ca/ca-1", resource.SSLCA{Spec: resource.SSLCASpec{CommonName: "infra root"}}); rec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body %s", rec.Code, rec.Body)
	}

	rec = do(t, mux, http.MethodGet, "/api/v1/ssl_ca/ca-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d", rec.Code)
	}

	rec = do(t, mux, http.MethodGet, "/api/v1/ssl_ca", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var list resource.List[resource.SSLCASpec, resource.SSLCAStatus]
	mustDecode(t, rec, &list)
	if len(list.Items) != 1 {
		t.Fatalf("list items = %d, want 1", len(list.Items))
	}
}

func TestSSLHandlerListPropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, _, _, store := newSSLEnv(t)
	if err := store.Put(resource.KindSSLCA+"/garbled", []byte("not json")); err != nil {
		t.Fatalf("seed corrupt entry: %v", err)
	}

	rec := do(t, mux, http.MethodGet, "/api/v1/ssl_ca", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("list status = %d, want 500", rec.Code)
	}
}

func TestSSLHandlerGetPropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, _, _, store := newSSLEnv(t)
	if err := store.Put(resource.KindSSLCA+"/ca-1", []byte("not json")); err != nil {
		t.Fatalf("seed corrupt entry: %v", err)
	}

	rec := do(t, mux, http.MethodGet, "/api/v1/ssl_ca/ca-1", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("get status = %d, want 500", rec.Code)
	}
}

func TestSSLHandlerPutRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	mux := newSSLMux(t)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/ssl_ca/ca-1", bytes.NewBufferString(`{"bogus": true}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field status = %d, want 400", rec.Code)
	}
}

func TestSSLHandlerPutPropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, _, _, _, dir := newSSLEnvWithDir(t)
	caDir := filepath.Join(dir, resource.KindSSLCA)
	if err := os.MkdirAll(caDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(caDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(caDir, 0o755) })

	rec := do(t, mux, http.MethodPut, "/api/v1/ssl_ca/ca-1", resource.SSLCA{Spec: resource.SSLCASpec{CommonName: "infra root"}})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("put status = %d, want 500", rec.Code)
	}
}

func TestSSLHandlerDeletePropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, _, _, _, dir := newSSLEnvWithDir(t)
	if rec := do(t, mux, http.MethodPut, "/api/v1/ssl_ca/ca-1", resource.SSLCA{Spec: resource.SSLCASpec{CommonName: "infra root"}}); rec.Code != http.StatusOK {
		t.Fatalf("seed create status = %d", rec.Code)
	}

	caDir := filepath.Join(dir, resource.KindSSLCA)
	if err := os.Chmod(caDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(caDir, 0o755) })

	rec := do(t, mux, http.MethodDelete, "/api/v1/ssl_ca/ca-1", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete status = %d, want 500", rec.Code)
	}
}

func TestSSLHandlerIssueRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	mux := newSSLMux(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ssl_ca/ca-1/issue", bytes.NewBufferString(`{"bogus": true}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field status = %d, want 400", rec.Code)
	}
}

func TestSSLHandlerIssuePropagatesLoadCAError(t *testing.T) {
	t.Parallel()

	mux, reg, _, _ := newSSLEnv(t)
	// A CA record whose encrypted key isn't even a well-formed envelope: Issue
	// must surface that as a server error, not "ca not found".
	if err := reg.Put(&resource.SSLCA{
		Metadata: resource.ObjectMeta{UID: "ca-1"},
		Status:   resource.SSLCAStatus{CertPEM: []byte("not a cert"), EncryptedKey: []byte("not an envelope")},
	}); err != nil {
		t.Fatalf("seed corrupt CA: %v", err)
	}

	rec := do(t, mux, http.MethodPost, "/api/v1/ssl_ca/ca-1/issue", issueBody{CommonName: "web"})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("issue status = %d, want 500", rec.Code)
	}
}

func TestSSLCertHandlerLifecycle(t *testing.T) {
	t.Parallel()

	mux := newSSLMux(t)
	if rec := do(t, mux, http.MethodPut, "/api/v1/ssl_ca/ca-1", resource.SSLCA{Spec: resource.SSLCASpec{CommonName: "infra root"}}); rec.Code != http.StatusOK {
		t.Fatalf("create CA status = %d, body %s", rec.Code, rec.Body)
	}

	rec := do(t, mux, http.MethodGet, "/api/v1/ssl_cert", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty list certs status = %d", rec.Code)
	}

	certBody := resource.SSLCert{Spec: resource.SSLCertSpec{CAID: "ca-1", CommonName: "web.example.com"}}
	rec = do(t, mux, http.MethodPut, "/api/v1/ssl_cert/cert-1", certBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("put cert status = %d, body %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "encryptedKey") {
		t.Fatalf("put cert response leaks key: %s", rec.Body)
	}

	rec = do(t, mux, http.MethodGet, "/api/v1/ssl_cert/cert-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get cert status = %d", rec.Code)
	}

	rec = do(t, mux, http.MethodGet, "/api/v1/ssl_cert/cert-1/reveal", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reveal cert status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "BEGIN CERTIFICATE") || !strings.Contains(rec.Body.String(), "PRIVATE KEY") {
		t.Fatalf("reveal cert missing cert/key: %s", rec.Body)
	}

	rec = do(t, mux, http.MethodGet, "/api/v1/ssl_cert", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list certs status = %d", rec.Code)
	}
	var list resource.List[resource.SSLCertSpec, resource.SSLCertStatus]
	mustDecode(t, rec, &list)
	if len(list.Items) != 1 {
		t.Fatalf("list certs = %d items, want 1", len(list.Items))
	}

	rec = do(t, mux, http.MethodDelete, "/api/v1/ssl_cert/cert-1", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete cert status = %d", rec.Code)
	}
	rec = do(t, mux, http.MethodGet, "/api/v1/ssl_cert/cert-1", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get-after-delete cert status = %d", rec.Code)
	}
}

func TestSSLCertHandlerPutRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	mux := newSSLMux(t)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/ssl_cert/cert-1", bytes.NewBufferString(`{"bogus": true}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field status = %d, want 400", rec.Code)
	}
}

func TestSSLCertHandlerPutRejectsInvalidSpec(t *testing.T) {
	t.Parallel()

	mux := newSSLMux(t)
	for _, tc := range []resource.SSLCertSpec{
		{CAID: "", CommonName: "web"},
		{CAID: "ca-1", CommonName: ""},
	} {
		rec := do(t, mux, http.MethodPut, "/api/v1/ssl_cert/cert-1", resource.SSLCert{Spec: tc})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("spec %+v status = %d, want 400", tc, rec.Code)
		}
	}
}

func TestSSLCertHandlerPutCANotFound(t *testing.T) {
	t.Parallel()

	mux := newSSLMux(t)
	rec := do(t, mux, http.MethodPut, "/api/v1/ssl_cert/cert-1", resource.SSLCert{Spec: resource.SSLCertSpec{CAID: "ghost", CommonName: "web"}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestSSLCertHandlerPutPropagatesCreateCertError(t *testing.T) {
	t.Parallel()

	mux := newSSLMux(t)
	if rec := do(t, mux, http.MethodPut, "/api/v1/ssl_ca/ca-1", resource.SSLCA{Spec: resource.SSLCASpec{CommonName: "infra root"}}); rec.Code != http.StatusOK {
		t.Fatalf("create CA status = %d", rec.Code)
	}

	rec := do(t, mux, http.MethodPut, "/api/v1/ssl_cert/cert-1", resource.SSLCert{Spec: resource.SSLCertSpec{
		CAID: "ca-1", CommonName: "web", IPAddresses: []string{"not-an-ip"},
	}})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body %s", rec.Code, rec.Body)
	}
}

func TestSSLCertHandlerGetMissingIsNotFound(t *testing.T) {
	t.Parallel()

	mux := newSSLMux(t)
	rec := do(t, mux, http.MethodGet, "/api/v1/ssl_cert/ghost", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestSSLCertHandlerGetPropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, _, _, store := newSSLEnv(t)
	if err := store.Put(resource.KindSSLCert+"/cert-1", []byte("not json")); err != nil {
		t.Fatalf("seed corrupt entry: %v", err)
	}

	rec := do(t, mux, http.MethodGet, "/api/v1/ssl_cert/cert-1", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestSSLCertHandlerRevealMissingIsNotFound(t *testing.T) {
	t.Parallel()

	mux := newSSLMux(t)
	rec := do(t, mux, http.MethodGet, "/api/v1/ssl_cert/ghost/reveal", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestSSLCertHandlerRevealPropagatesDecryptError(t *testing.T) {
	t.Parallel()

	mux, _, certs, _ := newSSLEnv(t)
	if err := certs.Put(&resource.SSLCert{
		Metadata: resource.ObjectMeta{UID: "cert-1"},
		Status:   resource.SSLCertStatus{CertPEM: []byte("not a cert"), EncryptedKey: []byte("not an envelope")},
	}); err != nil {
		t.Fatalf("seed corrupt cert: %v", err)
	}

	rec := do(t, mux, http.MethodGet, "/api/v1/ssl_cert/cert-1/reveal", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestSSLCertHandlerListPropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, _, _, store := newSSLEnv(t)
	if err := store.Put(resource.KindSSLCert+"/garbled", []byte("not json")); err != nil {
		t.Fatalf("seed corrupt entry: %v", err)
	}

	rec := do(t, mux, http.MethodGet, "/api/v1/ssl_cert", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestSSLCertHandlerDeletePropagatesStoreError(t *testing.T) {
	t.Parallel()

	mux, _, _, _, dir := newSSLEnvWithDir(t)
	if rec := do(t, mux, http.MethodPut, "/api/v1/ssl_ca/ca-1", resource.SSLCA{Spec: resource.SSLCASpec{CommonName: "infra root"}}); rec.Code != http.StatusOK {
		t.Fatalf("create CA status = %d", rec.Code)
	}
	if rec := do(t, mux, http.MethodPut, "/api/v1/ssl_cert/cert-1", resource.SSLCert{Spec: resource.SSLCertSpec{CAID: "ca-1", CommonName: "web"}}); rec.Code != http.StatusOK {
		t.Fatalf("create cert status = %d, body %s", rec.Code, rec.Body)
	}

	certDir := filepath.Join(dir, resource.KindSSLCert)
	if err := os.Chmod(certDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(certDir, 0o755) })

	rec := do(t, mux, http.MethodDelete, "/api/v1/ssl_cert/cert-1", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
