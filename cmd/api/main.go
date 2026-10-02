// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Command infra-api runs the declarative control-plane API server.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/goabonga/infrastructure/internal/auth"
	"github.com/goabonga/infrastructure/internal/crypto"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/httpsrv"
	"github.com/goabonga/infrastructure/internal/meta"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/ssl"
	"github.com/goabonga/infrastructure/internal/state"
)

func main() {
	addr := flag.String("addr", envOr("GOA_API_ADDR", ":8080"), "listen address")
	stateDir := flag.String("state-dir", envOr("GOA_STATE_DIR", "./state"), "state directory")
	stateDSN := flag.String("state-dsn", envOr("GOA_STATE_DSN", ""), "PostgreSQL DSN (enables the HA backend)")
	flag.Parse()

	store, err := state.Open(*stateDir, *stateDSN)
	if err != nil {
		log.Fatalf("infra-api: open state: %v", err)
	}

	var opts []httpsrv.Option
	if raw := os.Getenv("GOA_KMS_KEY"); raw != "" {
		kek, err := crypto.NewKEKFromBase64(raw)
		if err != nil {
			log.Fatalf("infra-api: GOA_KMS_KEY: %v", err)
		}
		opts = append(opts, httpsrv.WithSecretEncryption(kek))
		log.Print("infra-api: secret encryption enabled")
		// The platform's public root CA: created once, then kept across
		// restarts. Every machine and instance trusts it.
		cas := registry.New[resource.SSLCASpec, resource.SSLCAStatus](store, resource.KindSSLCA)
		certs := registry.New[resource.SSLCertSpec, resource.SSLCertStatus](store, resource.KindSSLCert)
		if _, err := ssl.NewService(cas, certs, kek).EnsureGlobalRoot(envOr("GOA_PUBLIC_ROOT_CN", "infra public root"), "infra"); err != nil {
			log.Fatalf("infra-api: public root CA: %v", err)
		}
	} else {
		log.Print("infra-api: GOA_KMS_KEY unset; secret routes disabled")
	}

	if authn := buildAuth(); authn != nil {
		opts = append(opts, httpsrv.WithAuth(authn))
	}

	srv := httpsrv.New(store, opts...)

	log.Printf("%s listening on %s (state dir %s)", meta.Line("infra-api", Version), *addr, *stateDir)
	if err := srv.ListenAndServe(*addr); err != nil {
		log.Fatalf("infra-api: %v", err)
	}
}

// buildAuth combines every authenticator the environment configures: a JWT
// verifier (GOA_API_JWT_PUBKEY), access-token introspection against infra-idp
// (GOA_API_IDP_INTROSPECT_URL), and static tokens (GOA_API_TOKENS). More than
// one may be set at once - e.g. so a browser's Phase-1 JWT and a Phase-3
// access token both work - in which case they are tried in order via
// auth.Chain. None configured leaves the API open.
func buildAuth() auth.Authenticator {
	var authns []auth.Authenticator

	if pubPEM := os.Getenv("GOA_API_JWT_PUBKEY"); pubPEM != "" {
		pub, err := auth.ParseECPublicKeyPEM([]byte(pubPEM))
		if err != nil {
			log.Fatalf("infra-api: GOA_API_JWT_PUBKEY: %v", err)
		}
		issuer := os.Getenv("GOA_API_JWT_ISSUER")
		if issuer == "" {
			log.Fatal("infra-api: GOA_API_JWT_ISSUER is required with GOA_API_JWT_PUBKEY")
		}
		log.Print("infra-api: JWT authentication enabled")
		authns = append(authns, auth.NewJWTAuthenticator(pub, issuer))
	}

	if introspectURL := os.Getenv("GOA_API_IDP_INTROSPECT_URL"); introspectURL != "" {
		clientID := os.Getenv("GOA_API_IDP_CLIENT_ID")
		clientSecret := os.Getenv("GOA_API_IDP_CLIENT_SECRET")
		if clientID == "" || clientSecret == "" {
			log.Fatal("infra-api: GOA_API_IDP_CLIENT_ID and GOA_API_IDP_CLIENT_SECRET are required with GOA_API_IDP_INTROSPECT_URL")
		}
		log.Print("infra-api: access-token introspection enabled")
		authns = append(authns, auth.NewIntrospectionAuthenticator(introspectURL, clientID, clientSecret))
	}

	if spec := os.Getenv("GOA_API_TOKENS"); spec != "" {
		tokens, err := auth.ParseTokens(spec)
		if err != nil {
			log.Fatalf("infra-api: GOA_API_TOKENS: %v", err)
		}
		log.Print("infra-api: static token authentication enabled")
		authns = append(authns, auth.NewTokenAuthenticator(tokens))
	}

	switch len(authns) {
	case 0:
		log.Print("infra-api: no auth configured; API is unauthenticated")
		return nil
	case 1:
		return authns[0]
	default:
		return auth.Chain(authns...)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
