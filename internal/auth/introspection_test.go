// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goabonga/infrastructure/internal/auth"
)

// fakeIntrospectionServer mimics infra-idp's RFC 7662 /introspect endpoint:
// it requires clientID/clientSecret over Basic auth and recognizes exactly
// one valid token.
func fakeIntrospectionServer(t *testing.T, clientID, clientSecret, validToken, subject string, roles []string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID, gotSecret, ok := r.BasicAuth()
		if !ok || gotID != clientID || gotSecret != clientSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		if r.PostForm.Get("token") != validToken {
			_ = json.NewEncoder(w).Encode(map[string]any{"active": false})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "sub": subject, "roles": roles})
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestIntrospectionAuthenticator(t *testing.T) {
	t.Parallel()

	ts := fakeIntrospectionServer(t, "infra-api", "s3cret", "infra_valid", "user-1", []string{"admin"})
	a := auth.NewIntrospectionAuthenticator(ts.URL, "infra-api", "s3cret")

	t.Run("valid token", func(t *testing.T) {
		t.Parallel()
		id, err := a.Authenticate(reqWithToken("infra_valid"))
		if err != nil {
			t.Fatalf("authenticate: %v", err)
		}
		if id.Subject != "user-1" || !id.HasRole("admin") {
			t.Fatalf("unexpected identity: %+v", id)
		}
	})

	t.Run("inactive token", func(t *testing.T) {
		t.Parallel()
		if _, err := a.Authenticate(reqWithToken("infra_revoked")); err == nil {
			t.Fatal("expected an inactive token to fail")
		}
	})

	t.Run("missing token", func(t *testing.T) {
		t.Parallel()
		if _, err := a.Authenticate(httptest.NewRequest(http.MethodGet, "/", nil)); err == nil {
			t.Fatal("expected a missing bearer token to fail")
		}
	})

	t.Run("wrong client credentials", func(t *testing.T) {
		t.Parallel()
		bad := auth.NewIntrospectionAuthenticator(ts.URL, "infra-api", "wrong")
		if _, err := bad.Authenticate(reqWithToken("infra_valid")); err == nil {
			t.Fatal("expected bad client credentials to fail")
		}
	})
}
