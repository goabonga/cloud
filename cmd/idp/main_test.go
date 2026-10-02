// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package main

import (
	"crypto/ecdsa"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/accesstoken"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/handler"
	"github.com/goabonga/infrastructure/internal/identity"
	"github.com/goabonga/infrastructure/internal/idp"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// newUsers returns a user Service backed by a fresh temp-dir file store.
func newUsers(t *testing.T) *identity.Service {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	return identity.NewService(registry.New[resource.UserSpec, resource.UserStatus](store, resource.KindUser))
}

func TestEnvOr(t *testing.T) {
	t.Setenv("GOA_IDP_TEST_ENVOR", "")
	if got := envOr("GOA_IDP_TEST_ENVOR", "default"); got != "default" {
		t.Fatalf("envOr() = %q, want %q", got, "default")
	}
	t.Setenv("GOA_IDP_TEST_ENVOR", "set")
	if got := envOr("GOA_IDP_TEST_ENVOR", "default"); got != "set" {
		t.Fatalf("envOr() = %q, want %q", got, "set")
	}
}

func TestLoadOrGenerateKeyFromEnv(t *testing.T) {
	want, err := idp.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemBytes, err := idp.MarshalPrivateKeyPEM(want)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	t.Setenv("GOA_IDP_KEY", string(pemBytes))

	got, err := loadOrGenerateKey()
	if err != nil {
		t.Fatalf("loadOrGenerateKey: %v", err)
	}
	if !got.Equal(want) {
		t.Fatal("loadOrGenerateKey did not return the key from GOA_IDP_KEY")
	}
}

func TestLoadOrGenerateKeyInvalidPEM(t *testing.T) {
	t.Setenv("GOA_IDP_KEY", "not a pem key")
	if _, err := loadOrGenerateKey(); err == nil {
		t.Fatal("expected an error for invalid GOA_IDP_KEY")
	}
}

func TestLoadOrGenerateKeyEphemeral(t *testing.T) {
	t.Setenv("GOA_IDP_KEY", "")
	key, err := loadOrGenerateKey()
	if err != nil {
		t.Fatalf("loadOrGenerateKey: %v", err)
	}
	if key == nil {
		t.Fatal("loadOrGenerateKey returned a nil key")
	}
}

func TestBootstrapAdminNoSpec(t *testing.T) {
	t.Setenv("GOA_IDP_BOOTSTRAP_ADMIN", "")
	users := newUsers(t)
	if err := bootstrapAdmin(users); err != nil {
		t.Fatalf("bootstrapAdmin: %v", err)
	}
	list, err := users.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected no users created, got %d", len(list))
	}
}

func TestBootstrapAdminStoreNotEmpty(t *testing.T) {
	users := newUsers(t)
	if _, err := users.Put("existing", resource.UserSpec{Username: "existing", Password: "s3cr3t"}); err != nil {
		t.Fatalf("seed existing user: %v", err)
	}
	t.Setenv("GOA_IDP_BOOTSTRAP_ADMIN", "admin:s3cr3t")
	if err := bootstrapAdmin(users); err != nil {
		t.Fatalf("bootstrapAdmin: %v", err)
	}
	list, err := users.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected store to remain untouched with 1 user, got %d", len(list))
	}
}

func TestBootstrapAdminCreatesUser(t *testing.T) {
	t.Setenv("GOA_IDP_BOOTSTRAP_ADMIN", "admin:s3cr3t")
	users := newUsers(t)
	if err := bootstrapAdmin(users); err != nil {
		t.Fatalf("bootstrapAdmin: %v", err)
	}
	list, err := users.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 user created, got %d", len(list))
	}
	if list[0].Spec.Username != "admin" {
		t.Fatalf("username = %q, want admin", list[0].Spec.Username)
	}
	found := false
	for _, role := range list[0].Spec.Roles {
		if role == handler.AdminRole {
			found = true
		}
	}
	if !found {
		t.Fatalf("roles = %v, want to include %q", list[0].Spec.Roles, handler.AdminRole)
	}
	if _, err := users.Authenticate("admin", "s3cr3t"); err != nil {
		t.Fatalf("authenticate bootstrapped admin: %v", err)
	}
}

func TestBootstrapAdminMalformedSpec(t *testing.T) {
	cases := []string{
		"no-colon-here",
		":password",
		"username:",
	}
	for _, spec := range cases {
		spec := spec
		t.Run(spec, func(t *testing.T) {
			users := newUsers(t)
			t.Setenv("GOA_IDP_BOOTSTRAP_ADMIN", spec)
			if err := bootstrapAdmin(users); err == nil {
				t.Fatalf("bootstrapAdmin(%q): expected error, got nil", spec)
			}
		})
	}
}

func TestBootstrapAdminListError(t *testing.T) {
	dir := t.TempDir()
	// "user" is resource.KindUser, the store prefix identity.Service lists
	// under; making it a plain file forces FileStore.List to fail with
	// something other than os.ErrNotExist.
	if err := os.WriteFile(filepath.Join(dir, resource.KindUser), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("seed blocking file: %v", err)
	}
	users := identity.NewService(registry.New[resource.UserSpec, resource.UserStatus](state.NewFileStore(dir), resource.KindUser))
	t.Setenv("GOA_IDP_BOOTSTRAP_ADMIN", "admin:s3cr3t")
	if err := bootstrapAdmin(users); err == nil {
		t.Fatal("expected bootstrapAdmin to fail when the user store can't be listed")
	}
}

func TestBootstrapAdminPutError(t *testing.T) {
	dir := t.TempDir()
	userDir := filepath.Join(dir, resource.KindUser)
	if err := os.MkdirAll(userDir, 0o500); err != nil { // r-x: list is fine, write is not
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(userDir, 0o700) })

	users := identity.NewService(registry.New[resource.UserSpec, resource.UserStatus](state.NewFileStore(dir), resource.KindUser))
	t.Setenv("GOA_IDP_BOOTSTRAP_ADMIN", "admin:s3cr3t")
	if err := bootstrapAdmin(users); err == nil {
		t.Fatal("expected bootstrapAdmin to fail when the user store can't be written to")
	}
}

func TestParseFlagsDefaults(t *testing.T) {
	t.Setenv("GOA_IDP_ADDR", "")
	t.Setenv("GOA_IDP_ISSUER", "")
	t.Setenv("GOA_IDP_STATE_DIR", "")
	t.Setenv("GOA_IDP_STATE_DSN", "")
	t.Setenv("GOA_WWW_URL", "")

	cfg, err := parseFlags(nil)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if cfg.addr != ":8081" {
		t.Errorf("addr = %q, want :8081", cfg.addr)
	}
	if cfg.issuerURL != "http://localhost:8081" {
		t.Errorf("issuerURL = %q, want http://localhost:8081", cfg.issuerURL)
	}
	if cfg.ttl != time.Hour {
		t.Errorf("ttl = %v, want 1h", cfg.ttl)
	}
	if cfg.stateDir != "./idp-state" {
		t.Errorf("stateDir = %q, want ./idp-state", cfg.stateDir)
	}
	if cfg.stateDSN != "" {
		t.Errorf("stateDSN = %q, want empty", cfg.stateDSN)
	}
	if cfg.consoleURL != "http://localhost:8088" {
		t.Errorf("consoleURL = %q, want http://localhost:8088", cfg.consoleURL)
	}
}

func TestParseFlagsFromEnv(t *testing.T) {
	t.Setenv("GOA_IDP_ADDR", ":9999")
	t.Setenv("GOA_IDP_ISSUER", "http://issuer.example")
	t.Setenv("GOA_IDP_STATE_DIR", "/tmp/custom-state")
	t.Setenv("GOA_IDP_STATE_DSN", "postgres://x")
	t.Setenv("GOA_WWW_URL", "http://console.example")

	cfg, err := parseFlags(nil)
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if cfg.addr != ":9999" {
		t.Errorf("addr = %q, want :9999", cfg.addr)
	}
	if cfg.issuerURL != "http://issuer.example" {
		t.Errorf("issuerURL = %q, want http://issuer.example", cfg.issuerURL)
	}
	if cfg.stateDir != "/tmp/custom-state" {
		t.Errorf("stateDir = %q, want /tmp/custom-state", cfg.stateDir)
	}
	if cfg.stateDSN != "postgres://x" {
		t.Errorf("stateDSN = %q, want postgres://x", cfg.stateDSN)
	}
	if cfg.consoleURL != "http://console.example" {
		t.Errorf("consoleURL = %q, want http://console.example", cfg.consoleURL)
	}
}

func TestParseFlagsOverridesFromArgs(t *testing.T) {
	cfg, err := parseFlags([]string{
		"-addr", ":7777",
		"-issuer", "http://args.example",
		"-ttl", "30m",
		"-state-dir", "/tmp/args-state",
		"-state-dsn", "etcd://1,2",
		"-console-url", "http://args-console.example",
	})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if cfg.addr != ":7777" {
		t.Errorf("addr = %q, want :7777", cfg.addr)
	}
	if cfg.issuerURL != "http://args.example" {
		t.Errorf("issuerURL = %q, want http://args.example", cfg.issuerURL)
	}
	if cfg.ttl != 30*time.Minute {
		t.Errorf("ttl = %v, want 30m", cfg.ttl)
	}
	if cfg.stateDir != "/tmp/args-state" {
		t.Errorf("stateDir = %q, want /tmp/args-state", cfg.stateDir)
	}
	if cfg.stateDSN != "etcd://1,2" {
		t.Errorf("stateDSN = %q, want etcd://1,2", cfg.stateDSN)
	}
	if cfg.consoleURL != "http://args-console.example" {
		t.Errorf("consoleURL = %q, want http://args-console.example", cfg.consoleURL)
	}
}

func TestParseFlagsInvalid(t *testing.T) {
	if _, err := parseFlags([]string{"-bogus-flag"}); err == nil {
		t.Fatal("expected an error for an unknown flag")
	}
	if _, err := parseFlags([]string{"-ttl", "not-a-duration"}); err == nil {
		t.Fatal("expected an error for an invalid duration")
	}
}

// newTestServer builds a minimal, real idp.Server for exercising serve/serveListener.
func newTestServer(t *testing.T) *idp.Server {
	t.Helper()
	key, err := idp.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	users := newUsers(t)
	accessTokens := accesstoken.NewService(
		registry.New[resource.AccessTokenSpec, resource.AccessTokenStatus](state.NewFileStore(t.TempDir()), resource.KindAccessToken),
		users,
	)
	return idp.NewServer(idp.NewIssuer(key, "http://localhost", time.Hour), map[string]string{}, users, accessTokens, &key.PublicKey, "http://localhost", "http://localhost")
}

func TestServeListener(t *testing.T) {
	srv := newTestServer(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- serveListener(srv, ln)
	}()

	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	if err := <-errCh; err == nil {
		t.Fatal("expected serveListener to return an error once the listener closed")
	}
}

func TestServeSuccess(t *testing.T) {
	srv := newTestServer(t)

	lnCh := make(chan net.Listener, 1)
	original := netListen
	netListen = func(network, address string) (net.Listener, error) {
		ln, err := original(network, address)
		if err == nil {
			lnCh <- ln
		}
		return ln, err
	}
	t.Cleanup(func() { netListen = original })

	errCh := make(chan error, 1)
	go func() {
		errCh <- serve(srv, "127.0.0.1:0")
	}()

	ln := <-lnCh
	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	if err := <-errCh; err == nil {
		t.Fatal("expected serve to return an error once the listener closed")
	}
}

func TestServeListenError(t *testing.T) {
	srv := newTestServer(t)
	if err := serve(srv, "invalid-address-with-no-port"); err == nil {
		t.Fatal("expected serve to fail on an invalid address")
	}
}

func TestServeAddressInUse(t *testing.T) {
	srv := newTestServer(t)

	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = held.Close() }()

	if err := serve(srv, held.Addr().String()); err == nil {
		t.Fatal("expected serve to fail binding an address already in use")
	}
}

func TestRunInvalidFlags(t *testing.T) {
	if err := run([]string{"-bogus-flag"}); err == nil {
		t.Fatal("expected run to fail on an invalid flag")
	}
}

func TestRunInvalidKey(t *testing.T) {
	t.Setenv("GOA_IDP_KEY", "not a pem key")
	if err := run(nil); err == nil {
		t.Fatal("expected run to fail on an invalid GOA_IDP_KEY")
	}
}

func TestRunInvalidClients(t *testing.T) {
	t.Setenv("GOA_IDP_KEY", "")
	t.Setenv("GOA_IDP_CLIENTS", "malformed-no-colon")
	if err := run(nil); err == nil {
		t.Fatal("expected run to fail on malformed GOA_IDP_CLIENTS")
	}
}

func TestRunMarshalPublicKeyError(t *testing.T) {
	original := marshalPublicKeyPEM
	marshalPublicKeyPEM = func(*ecdsa.PublicKey) ([]byte, error) {
		return nil, fmt.Errorf("boom")
	}
	t.Cleanup(func() { marshalPublicKeyPEM = original })

	t.Setenv("GOA_IDP_KEY", "")
	t.Setenv("GOA_IDP_CLIENTS", "")
	t.Setenv("GOA_IDP_STATE_DIR", t.TempDir())
	t.Setenv("GOA_IDP_STATE_DSN", "")
	t.Setenv("GOA_IDP_BOOTSTRAP_ADMIN", "")

	if err := run(nil); err == nil {
		t.Fatal("expected run to fail when marshaling the public key fails")
	}
}

func TestRunStateOpenError(t *testing.T) {
	t.Setenv("GOA_IDP_KEY", "")
	t.Setenv("GOA_IDP_CLIENTS", "")
	t.Setenv("GOA_IDP_STATE_DIR", t.TempDir())
	// A non-etcd, non-empty DSN routes to the PostgreSQL backend; this one is
	// syntactically invalid, so connecting fails fast without a real database.
	t.Setenv("GOA_IDP_STATE_DSN", "postgres://invalid:[::::]/bad")
	if err := run(nil); err == nil {
		t.Fatal("expected run to fail opening an invalid state backend")
	}
}

func TestRunInvalidBootstrapAdmin(t *testing.T) {
	t.Setenv("GOA_IDP_KEY", "")
	t.Setenv("GOA_IDP_CLIENTS", "")
	t.Setenv("GOA_IDP_STATE_DIR", t.TempDir())
	t.Setenv("GOA_IDP_STATE_DSN", "")
	t.Setenv("GOA_IDP_BOOTSTRAP_ADMIN", "malformed-no-colon")
	if err := run(nil); err == nil {
		t.Fatal("expected run to fail on malformed GOA_IDP_BOOTSTRAP_ADMIN")
	}
}

// TestRunBindFailure drives run through its full wiring (key, clients, state,
// bootstrap, server construction, logging) and only fails at the very last
// step - binding the listen address - because that address is already held.
// This exercises every statement in run without ever blocking on a real
// accept loop.
func TestRunBindFailure(t *testing.T) {
	key, err := idp.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemBytes, err := idp.MarshalPrivateKeyPEM(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = held.Close() }()

	t.Setenv("GOA_IDP_KEY", string(pemBytes))
	t.Setenv("GOA_IDP_CLIENTS", "")
	t.Setenv("GOA_IDP_STATE_DIR", t.TempDir())
	t.Setenv("GOA_IDP_STATE_DSN", "")
	t.Setenv("GOA_IDP_BOOTSTRAP_ADMIN", "")

	err = run([]string{"-addr", held.Addr().String()})
	if err == nil {
		t.Fatal("expected run to fail binding an address already in use")
	}
	if !strings.Contains(err.Error(), "address already in use") && !strings.Contains(err.Error(), "bind") {
		t.Logf("run() error (informational): %v", err)
	}
}
