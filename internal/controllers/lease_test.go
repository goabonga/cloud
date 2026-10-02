// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package controllers_test

import (
	"context"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/controllers"
	"github.com/goabonga/infrastructure/internal/state"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestLeaseAcquireRenewStealRelease(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := state.NewFileStore(t.TempDir())
	clk := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}

	a := controllers.NewLease(store, "leases/x", "A", 10*time.Second, clk.now)
	b := controllers.NewLease(store, "leases/x", "B", 10*time.Second, clk.now)

	if ok, err := a.Acquire(ctx); err != nil || !ok {
		t.Fatalf("A acquire: ok=%v err=%v", ok, err)
	}
	if ok, err := b.Acquire(ctx); err != nil || ok {
		t.Fatalf("B should not acquire while A holds: ok=%v err=%v", ok, err)
	}
	if ok, err := a.Acquire(ctx); err != nil || !ok {
		t.Fatalf("A renew: ok=%v err=%v", ok, err)
	}

	// Let A's lease expire; B steals it.
	clk.advance(11 * time.Second)
	if ok, err := b.Acquire(ctx); err != nil || !ok {
		t.Fatalf("B should steal expired lease: ok=%v err=%v", ok, err)
	}
	if ok, err := a.Acquire(ctx); err != nil || ok {
		t.Fatalf("A should not reacquire while B holds: ok=%v err=%v", ok, err)
	}

	// B releases; A can take it again.
	if err := b.Release(ctx); err != nil {
		t.Fatalf("B release: %v", err)
	}
	if ok, err := a.Acquire(ctx); err != nil || !ok {
		t.Fatalf("A acquire after release: ok=%v err=%v", ok, err)
	}
}

func TestLeaseReleaseNotOwnerIsNoop(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := state.NewFileStore(t.TempDir())
	clk := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}

	a := controllers.NewLease(store, "leases/x", "A", 10*time.Second, clk.now)
	b := controllers.NewLease(store, "leases/x", "B", 10*time.Second, clk.now)

	if _, err := a.Acquire(ctx); err != nil {
		t.Fatalf("A acquire: %v", err)
	}
	// B does not own the lease; releasing must not drop A's.
	if err := b.Release(ctx); err != nil {
		t.Fatalf("B release: %v", err)
	}
	if ok, err := b.Acquire(ctx); err != nil || ok {
		t.Fatalf("A's lease should survive B's release: ok=%v err=%v", ok, err)
	}
}
