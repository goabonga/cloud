// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Command infra-idp is the identity provider: it issues ES256 JWTs to clients
// via the OAuth2 client-credentials grant and publishes its public key.
package main

import (
	"crypto/ecdsa"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/goabonga/infrastructure/internal/accesstoken"
	"github.com/goabonga/infrastructure/internal/auth"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/handler"
	"github.com/goabonga/infrastructure/internal/httpsec"
	"github.com/goabonga/infrastructure/internal/identity"
	"github.com/goabonga/infrastructure/internal/idp"
	"github.com/goabonga/infrastructure/internal/meta"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("infra-idp: %v", err)
	}
}

// config holds the parsed command-line flags (each defaulting to its
// environment variable, then a hardcoded fallback).
type config struct {
	addr       string
	issuerURL  string
	ttl        time.Duration
	stateDir   string
	stateDSN   string
	consoleURL string
}

// parseFlags parses args (excluding the program name) into a config. It uses
// a dedicated FlagSet rather than the flag package's global state so it can
// be exercised with arbitrary args in tests.
func parseFlags(args []string) (*config, error) {
	fs := flag.NewFlagSet("infra-idp", flag.ContinueOnError)
	cfg := &config{}
	fs.StringVar(&cfg.addr, "addr", envOr("GOA_IDP_ADDR", ":8081"), "listen address")
	fs.StringVar(&cfg.issuerURL, "issuer", envOr("GOA_IDP_ISSUER", "http://localhost:8081"), "issuer URL")
	fs.DurationVar(&cfg.ttl, "ttl", time.Hour, "token lifetime")
	fs.StringVar(&cfg.stateDir, "state-dir", envOr("GOA_IDP_STATE_DIR", "./idp-state"), "state directory")
	fs.StringVar(&cfg.stateDSN, "state-dsn", envOr("GOA_IDP_STATE_DSN", ""), "PostgreSQL DSN (enables the HA backend)")
	fs.StringVar(&cfg.consoleURL, "console-url", envOr("GOA_WWW_URL", "http://localhost:8088"), "console URL where a human approves a device authorization")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return cfg, nil
}

func run(args []string) error {
	cfg, err := parseFlags(args)
	if err != nil {
		return err
	}

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

	store, err := state.Open(cfg.stateDir, cfg.stateDSN)
	if err != nil {
		return err
	}
	users := identity.NewService(registry.New[resource.UserSpec, resource.UserStatus](store, resource.KindUser))
	if err := bootstrapAdmin(users); err != nil {
		return err
	}
	accessTokens := accesstoken.NewService(registry.New[resource.AccessTokenSpec, resource.AccessTokenStatus](store, resource.KindAccessToken), users)

	server := idp.NewServer(idp.NewIssuer(key, cfg.issuerURL, cfg.ttl), clients, users, accessTokens, &key.PublicKey, cfg.issuerURL, cfg.consoleURL, idp.WithDeviceState(store))

	log.Printf("%s listening on %s (issuer %s)", meta.Line("infra-idp", Version), cfg.addr, cfg.issuerURL)
	pub, err := marshalPublicKeyPEM(&key.PublicKey)
	if err != nil {
		return err
	}
	log.Printf("infra-idp: verification public key:\n%s", pub)

	return serve(server, cfg.addr)
}

// netListen is net.Listen, overridable in tests so they can observe the
// listener a successful serve call creates.
var netListen = net.Listen

// marshalPublicKeyPEM is idp.MarshalPublicKeyPEM, overridable in tests to
// exercise run's error handling around it without a real key that can fail
// to marshal.
var marshalPublicKeyPEM = idp.MarshalPublicKeyPEM

// serve listens on addr and serves the IdP until the listener errors or is
// closed. Split out from serveListener so a test can inject its own
// net.Listener (e.g. bound to an ephemeral port) instead of a fixed address.
func serve(server *idp.Server, addr string) error {
	ln, err := netListen("tcp", addr)
	if err != nil {
		return err
	}
	return serveListener(server, ln)
}

// serveListener runs server's handler on ln until serving stops.
func serveListener(server *idp.Server, ln net.Listener) error {
	httpServer := &http.Server{Handler: server.Handler(), ReadHeaderTimeout: 10 * time.Second}
	httpsec.ConfigureServer(httpServer)
	return httpServer.Serve(ln)
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
		return fmt.Errorf("infra-idp: GOA_IDP_BOOTSTRAP_ADMIN must be \"username:password\"")
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
