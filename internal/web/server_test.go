// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package web_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/goabonga/infrastructure/internal/web"
)

func newServer(t *testing.T, apiURL string) http.Handler {
	t.Helper()
	return newServerWithIDP(t, apiURL, "")
}

func newServerWithIDP(t *testing.T, apiURL, idpURL string) http.Handler {
	t.Helper()
	static := fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>infra</title>")},
		"assets/app.js": {Data: []byte("console.log('app')")},
	}
	srv, err := web.New(static, web.Options{APIBaseURL: apiURL, IDPBaseURL: idpURL})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return srv.Handler()
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestServesIndexAndAssets(t *testing.T) {
	t.Parallel()

	h := newServer(t, "")

	if rec := get(t, h, "/"); rec.Code != http.StatusOK || !contains(rec, "infra") {
		t.Fatalf("index: code=%d body=%q", rec.Code, rec.Body)
	}
	if rec := get(t, h, "/assets/app.js"); rec.Code != http.StatusOK || !contains(rec, "app") {
		t.Fatalf("asset: code=%d body=%q", rec.Code, rec.Body)
	}
}

func TestSPAFallback(t *testing.T) {
	t.Parallel()

	// An unknown client-side route serves index.html, not a 404.
	rec := get(t, newServer(t, ""), "/vpcs")
	if rec.Code != http.StatusOK || !contains(rec, "infra") {
		t.Fatalf("fallback: code=%d body=%q", rec.Code, rec.Body)
	}
}

func TestHealthAndSecurityTxt(t *testing.T) {
	t.Parallel()

	h := newServer(t, "")
	if rec := get(t, h, "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("healthz: %d", rec.Code)
	}
	if rec := get(t, h, "/.well-known/security.txt"); rec.Code != http.StatusOK || !contains(rec, "Contact:") {
		t.Fatalf("security.txt: code=%d body=%q", rec.Code, rec.Body)
	}
}

func TestProxiesAPI(t *testing.T) {
	t.Parallel()

	var gotPath, gotAuth string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	}))
	defer backend.Close()

	h := newServer(t, backend.URL)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/vpc", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("proxy status = %d", rec.Code)
	}
	if gotPath != "/api/v1/vpc" {
		t.Fatalf("proxied path = %q", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("authorization not forwarded: %q", gotAuth)
	}
}

func TestProxiesIDPWithPrefixStripped(t *testing.T) {
	t.Parallel()

	var gotPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	h := newServerWithIDP(t, "", backend.URL)
	rec := get(t, h, "/idp/login")

	if rec.Code != http.StatusOK {
		t.Fatalf("proxy status = %d", rec.Code)
	}
	if gotPath != "/login" {
		t.Fatalf("proxied path = %q, want /login (prefix stripped)", gotPath)
	}
}

func contains(rec *httptest.ResponseRecorder, sub string) bool {
	body, _ := io.ReadAll(rec.Body)
	return strings.Contains(string(body), sub)
}
