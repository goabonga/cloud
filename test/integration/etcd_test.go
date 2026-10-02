// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

//go:build integration

package integration

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/state"
)

// TestEtcdStore exercises the etcd backend end to end. It needs reachable
// endpoints via ETCD_ENDPOINTS (comma-separated host:port) and skips otherwise.
func TestEtcdStore(t *testing.T) {
	endpoints := os.Getenv("ETCD_ENDPOINTS")
	if endpoints == "" {
		t.Skip("set ETCD_ENDPOINTS to run the etcd integration test")
	}

	s, err := state.NewEtcdStore(strings.Split(endpoints, ","))
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
