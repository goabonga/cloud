// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package auth_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/auth"
)

func TestChainTriesEachUntilSuccess(t *testing.T) {
	t.Parallel()

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	const issuer = "http://idp"
	jwtAuth := auth.NewJWTAuthenticator(&key.PublicKey, issuer)

	ts := fakeIntrospectionServer(t, "infra-api", "s3cret", "infra_valid", "service-1", []string{"ci"})
	introspectionAuth := auth.NewIntrospectionAuthenticator(ts.URL, "infra-api", "s3cret")

	chained := auth.Chain(jwtAuth, introspectionAuth)

	t.Run("JWT-shaped token", func(t *testing.T) {
		t.Parallel()
		tok := mintToken(t, key, "alice", issuer, time.Now().Add(time.Hour))
		id, err := chained.Authenticate(reqWithToken(tok))
		if err != nil {
			t.Fatalf("authenticate: %v", err)
		}
		if id.Subject != "alice" {
			t.Fatalf("subject = %q, want alice", id.Subject)
		}
	})

	t.Run("opaque access token falls through to introspection", func(t *testing.T) {
		t.Parallel()
		id, err := chained.Authenticate(reqWithToken("infra_valid"))
		if err != nil {
			t.Fatalf("authenticate: %v", err)
		}
		if id.Subject != "service-1" || !id.HasRole("ci") {
			t.Fatalf("unexpected identity: %+v", id)
		}
	})

	t.Run("neither accepts it", func(t *testing.T) {
		t.Parallel()
		if _, err := chained.Authenticate(reqWithToken("infra_unknown")); err == nil {
			t.Fatal("expected failure when no authenticator accepts the token")
		}
	})
}
