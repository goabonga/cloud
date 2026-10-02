// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goabonga/infrastructure/internal/auth"
)

func TestTokenAuthenticator(t *testing.T) {
	t.Parallel()

	a := auth.NewTokenAuthenticator(map[string]string{"good-token": "alice"})

	tests := []struct {
		name   string
		header string
		ok     bool
	}{
		{"valid", "Bearer good-token", true},
		{"valid case-insensitive scheme", "bearer good-token", true},
		{"unknown token", "Bearer nope", false},
		{"missing header", "", false},
		{"wrong scheme", "Basic good-token", false},
		{"empty token", "Bearer ", false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			id, err := a.Authenticate(r)
			if tc.ok {
				if err != nil {
					t.Fatalf("expected success, got %v", err)
				}
				if id.Subject != "alice" {
					t.Fatalf("subject = %q, want alice", id.Subject)
				}
			} else if err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestParseTokens(t *testing.T) {
	t.Parallel()

	got, err := auth.ParseTokens("t1:alice, t2:bob")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got["t1"] != "alice" || got["t2"] != "bob" {
		t.Fatalf("unexpected tokens: %v", got)
	}

	for _, bad := range []string{"", "no-colon", "t1:", ":subject"} {
		if _, err := auth.ParseTokens(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestMiddleware(t *testing.T) {
	t.Parallel()

	a := auth.NewTokenAuthenticator(map[string]string{"good": "alice"})
	var gotSubject string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := auth.IdentityFrom(r.Context()); ok {
			gotSubject = id.Subject
		}
		w.WriteHeader(http.StatusOK)
	})
	h := auth.Middleware(a, next)

	// Authenticated.
	r := httptest.NewRequest(http.MethodGet, "/api/v1/vpc", nil)
	r.Header.Set("Authorization", "Bearer good")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK || gotSubject != "alice" {
		t.Fatalf("authenticated request failed: code=%d subject=%q", rec.Code, gotSubject)
	}

	// Unauthenticated.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/vpc", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated code = %d, want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("missing WWW-Authenticate challenge")
	}
}
