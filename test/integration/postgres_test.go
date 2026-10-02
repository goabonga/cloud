// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/state"
)

// TestPostgresStore exercises the PostgreSQL backend end to end. It needs a
// reachable database via POSTGRES_DSN and skips otherwise.
func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_DSN to run the postgres integration test")
	}

	s, err := state.NewPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = s.Close() }()

	prefix := fmt.Sprintf("ittest-%d", time.Now().UnixNano())
	key := prefix + "/k1"
	t.Cleanup(func() {
		_ = s.Delete(key)
		_ = s.Delete(prefix + "/k2")
	})

	// Missing key.
	if _, err := s.Get(key); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// Put / Get.
	if err := s.Put(key, []byte("v1")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if got, err := s.Get(key); err != nil || string(got) != "v1" {
		t.Fatalf("get = %q, %v", got, err)
	}

	// CompareAndSwap: stale rejected, correct swaps, create-when-absent.
	if ok, err := s.CompareAndSwap(key, []byte("stale"), []byte("v2")); err != nil || ok {
		t.Fatalf("stale cas: ok=%v err=%v", ok, err)
	}
	if ok, err := s.CompareAndSwap(key, []byte("v1"), []byte("v2")); err != nil || !ok {
		t.Fatalf("swap cas: ok=%v err=%v", ok, err)
	}
	if ok, err := s.CompareAndSwap(prefix+"/k2", nil, []byte("created")); err != nil || !ok {
		t.Fatalf("create cas: ok=%v err=%v", ok, err)
	}

	// List returns both entries directly under the prefix.
	kvs, err := s.List(prefix)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(kvs) != 2 {
		t.Fatalf("list len = %d, want 2", len(kvs))
	}

	// Delete then gone.
	if err := s.Delete(key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(key); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
