// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package state_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/state"
)

// newEtcdStore returns a store connected to the shared ephemeral container
// started by TestMain, skipping the test if that container is not
// available.
func newEtcdStore(t *testing.T) *state.EtcdStore {
	t.Helper()
	ensureEtcdContainer()
	if !etcdAvailable {
		t.Skip("etcd container is not available")
	}
	s, err := state.NewEtcdStore([]string{etcdEndpoint})
	if err != nil {
		t.Fatalf("new etcd store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestEtcdStoreNewDialsLazily(t *testing.T) {
	t.Parallel()

	// The client dials lazily, so New succeeds even against an
	// unreachable endpoint.
	s, err := state.NewEtcdStore([]string{"127.0.0.1:1"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer func() { _ = s.Close() }()
}

func TestEtcdStoreCRUD(t *testing.T) {
	t.Parallel()
	s := newEtcdStore(t)

	key := fmt.Sprintf("etcdtest-%d/k1", time.Now().UnixNano())

	if _, err := s.Get(key); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	if err := s.Put(key, []byte("v1")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if got, err := s.Get(key); err != nil || string(got) != "v1" {
		t.Fatalf("get = %q, %v", got, err)
	}

	if err := s.Put(key, []byte("v1b")); err != nil {
		t.Fatalf("put overwrite: %v", err)
	}
	if got, err := s.Get(key); err != nil || string(got) != "v1b" {
		t.Fatalf("get after overwrite = %q, %v", got, err)
	}

	if err := s.Delete(key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(key); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	// Deleting a missing key is not an error.
	if err := s.Delete(key); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestEtcdStoreList(t *testing.T) {
	t.Parallel()
	s := newEtcdStore(t)

	prefix := fmt.Sprintf("etcdtest-list-%d", time.Now().UnixNano())
	for _, k := range []string{"a", "b"} {
		if err := s.Put(prefix+"/"+k, []byte(k)); err != nil {
			t.Fatalf("put %s: %v", k, err)
		}
	}
	// A deeper key must not appear in the direct listing.
	if err := s.Put(prefix+"/sub/c", []byte("c")); err != nil {
		t.Fatalf("put nested: %v", err)
	}

	kvs, err := s.List(prefix)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(kvs) != 2 {
		t.Fatalf("list len = %d, want 2", len(kvs))
	}
	for _, kv := range kvs {
		if kv.Key != prefix+"/a" && kv.Key != prefix+"/b" {
			t.Fatalf("unexpected key %q", kv.Key)
		}
	}
}

func TestEtcdStoreListEmpty(t *testing.T) {
	t.Parallel()
	s := newEtcdStore(t)

	kvs, err := s.List(fmt.Sprintf("etcdtest-empty-%d", time.Now().UnixNano()))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(kvs) != 0 {
		t.Fatalf("expected no entries, got %d", len(kvs))
	}
}

func TestEtcdStoreCompareAndSwap(t *testing.T) {
	t.Parallel()
	s := newEtcdStore(t)

	key := fmt.Sprintf("etcdtest-cas-%d/k", time.Now().UnixNano())

	// Create only when absent.
	ok, err := s.CompareAndSwap(key, nil, []byte("v1"))
	if err != nil || !ok {
		t.Fatalf("create cas: ok=%v err=%v", ok, err)
	}

	// Recreating when already present (oldValue nil) is rejected.
	ok, err = s.CompareAndSwap(key, nil, []byte("v2"))
	if err != nil {
		t.Fatalf("recreate cas err: %v", err)
	}
	if ok {
		t.Fatal("expected recreate cas to fail")
	}

	// Stale oldValue is rejected.
	ok, err = s.CompareAndSwap(key, []byte("stale"), []byte("v2"))
	if err != nil {
		t.Fatalf("mismatch cas err: %v", err)
	}
	if ok {
		t.Fatal("expected mismatch cas to fail")
	}

	// Correct oldValue swaps.
	ok, err = s.CompareAndSwap(key, []byte("v1"), []byte("v2"))
	if err != nil || !ok {
		t.Fatalf("swap cas: ok=%v err=%v", ok, err)
	}
	got, err := s.Get(key)
	if err != nil || string(got) != "v2" {
		t.Fatalf("get after swap = %q, %v", got, err)
	}
}

func TestEtcdStoreClosedClientErrors(t *testing.T) {
	t.Parallel()
	s := newEtcdStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Every method must surface a connection error once the client is
	// closed, instead of panicking or silently succeeding.
	if _, err := s.Get("anykey"); err == nil {
		t.Fatal("expected error from Get on a closed client")
	}
	if err := s.Put("anykey", []byte("v")); err == nil {
		t.Fatal("expected error from Put on a closed client")
	}
	if err := s.Delete("anykey"); err == nil {
		t.Fatal("expected error from Delete on a closed client")
	}
	if _, err := s.List("anykey"); err == nil {
		t.Fatal("expected error from List on a closed client")
	}
	if _, err := s.CompareAndSwap("anykey", nil, []byte("v")); err == nil {
		t.Fatal("expected error from CompareAndSwap on a closed client")
	}
}
