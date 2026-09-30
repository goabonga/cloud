// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Command infra-idp is the identity provider: it issues ES256 JWTs to clients
// via the OAuth2 client-credentials grant and publishes its public key.
package main

import (
	"crypto/ecdsa"
	"flag"
	"log"
	"os"
	"strings"
	"time"

	"github.com/goabonga/infrastructure/internal/auth"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/handler"
	"github.com/goabonga/infrastructure/internal/identity"
	"github.com/goabonga/infrastructure/internal/idp"
	"github.com/goabonga/infrastructure/internal/meta"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("infra-idp: %v", err)
	}
}

func run() error {
	addr := flag.String("addr", envOr("GOA_IDP_ADDR", ":8081"), "listen address")
	issuerURL := flag.String("issuer", envOr("GOA_IDP_ISSUER", "http://localhost:8081"), "issuer URL")
	ttl := flag.Duration("ttl", time.Hour, "token lifetime")
	stateDir := flag.String("state-dir", envOr("GOA_IDP_STATE_DIR", "./idp-state"), "state directory")
	stateDSN := flag.String("state-dsn", envOr("GOA_IDP_STATE_DSN", ""), "PostgreSQL DSN (enables the HA backend)")
	consoleURL := flag.String("console-url", envOr("GOA_WWW_URL", "http://localhost:8088"), "console URL where a human approves a device authorization")
	flag.Parse()

	key, err := loadOrGenerateKey()
	if err != nil {
		return err
	}

	clients := map[string]string{}
	if spec := os.Getenv("GOA_IDP_CLIENTS"); spec != "" {
		clients, err = auth.ParseTokens(spec)
		if err != nil {
			return err
		}
	}

	store, err := state.Open(*stateDir, *stateDSN)
	if err != nil {
		return err
	}
	users := identity.NewService(registry.New[resource.UserSpec, resource.UserStatus](store, resource.KindUser))
	if err := bootstrapAdmin(users); err != nil {
		return err
	}

	server := idp.NewServer(idp.NewIssuer(key, *issuerURL, *ttl), clients, users, &key.PublicKey, *issuerURL, *consoleURL)

	log.Printf("%s listening on %s (issuer %s)", meta.Line("infra-idp", Version), *addr, *issuerURL)
	pub, err := idp.MarshalPublicKeyPEM(&key.PublicKey)
	if err != nil {
		return err
	}
	log.Printf("infra-idp: verification public key:\n%s", pub)

	return server.ListenAndServe(*addr)
}

// loadOrGenerateKey reads the signing key from GOA_IDP_KEY (PEM) or generates an
// ephemeral one, warning that tokens will not survive a restart.
func loadOrGenerateKey() (*ecdsa.PrivateKey, error) {
	if raw := os.Getenv("GOA_IDP_KEY"); raw != "" {
		return idp.ParsePrivateKeyPEM([]byte(raw))
	}
	log.Print("infra-idp: GOA_IDP_KEY unset; generating an ephemeral signing key")
	return idp.GenerateKey()
}

// bootstrapAdmin seeds one admin user from GOA_IDP_BOOTSTRAP_ADMIN
// ("username:password"), but only when the user store is empty - otherwise
// there is no way to create the first user through the admin-gated API.
func bootstrapAdmin(users *identity.Service) error {
	spec := os.Getenv("GOA_IDP_BOOTSTRAP_ADMIN")
	if spec == "" {
		return nil
	}
	existing, err := users.List()
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	username, password, ok := strings.Cut(spec, ":")
	if !ok || username == "" || password == "" {
		log.Fatal("infra-idp: GOA_IDP_BOOTSTRAP_ADMIN must be \"username:password\"")
	}
	if _, err := users.Put(username, resource.UserSpec{Username: username, Password: password, Roles: []string{handler.AdminRole}}); err != nil {
		return err
	}
	log.Printf("infra-idp: bootstrapped admin user %q", username) // #nosec G706 -- username comes from GOA_IDP_BOOTSTRAP_ADMIN, set by the operator starting this process, not request input
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
