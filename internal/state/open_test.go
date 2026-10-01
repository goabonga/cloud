// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package state_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/state"
)

func TestOpenFileStore(t *testing.T) {
	t.Parallel()

	// With no DSN, Open returns a working file-backed store.
	store, err := state.Open(t.TempDir(), "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Put("k", []byte("v")); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := store.Get("k")
	if err != nil || string(got) != "v" {
		t.Fatalf("get = %q, %v", got, err)
	}
}

func TestOpenEtcdScheme(t *testing.T) {
	t.Parallel()

	// An "etcd://" DSN selects the etcd backend. The client dials lazily, so
	// Open succeeds without a reachable server.
	store, err := state.Open("", "etcd://127.0.0.1:2379,127.0.0.1:12379")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = store.Close() }()

	if _, ok := store.(*state.EtcdStore); !ok {
		t.Fatalf("Open returned %T, want *state.EtcdStore", store)
	}
}

func TestOpenPostgresDSN(t *testing.T) {
	t.Parallel()
	ensurePostgresContainer()
	if !postgresAvailable {
		t.Skip("postgres container is not available")
	}

	// Any non-empty dsn that isn't the etcd scheme selects the postgres
	// backend.
	store, err := state.Open("", postgresDSN)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = store.Close() }()

	if _, ok := store.(*state.PostgresStore); !ok {
		t.Fatalf("Open returned %T, want *state.PostgresStore", store)
	}

	if err := store.Put("k", []byte("v")); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := store.Get("k")
	if err != nil || string(got) != "v" {
		t.Fatalf("get = %q, %v", got, err)
	}
	_ = store.Delete("k")
}

func TestOpenPostgresRejectsBadDSN(t *testing.T) {
	t.Parallel()

	// Open must surface the underlying connection error for the postgres
	// branch too, not just construct a store blindly.
	if _, err := state.Open("", "://not a dsn"); err == nil {
		t.Fatal("expected error for a malformed dsn")
	}
}
