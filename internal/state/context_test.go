// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/state"
)

func TestStoreContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store, err := state.NewEtcdStore([]string{"127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	bound := state.WithContext(ctx, store)
	if _, err := bound.Get("k"); !errors.Is(err, context.Canceled) {
		t.Fatalf("get: %v", err)
	}
	if err := bound.Put("k", []byte("v")); !errors.Is(err, context.Canceled) {
		t.Fatalf("put: %v", err)
	}
	if _, err := bound.List("k"); !errors.Is(err, context.Canceled) {
		t.Fatalf("list: %v", err)
	}
	if err := bound.Delete("k"); !errors.Is(err, context.Canceled) {
		t.Fatalf("delete: %v", err)
	}
	if _, err := bound.CompareAndSwap("k", nil, []byte("v")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cas: %v", err)
	}
}

func TestEtcdOperationHonorsRequestDeadline(t *testing.T) {
	store, err := state.NewEtcdStore([]string{"127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = state.WithContext(ctx, store).Get("k")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("request deadline was ignored")
	}
}
