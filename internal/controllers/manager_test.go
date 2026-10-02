// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package controllers_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/controllers"
	"github.com/goabonga/infrastructure/internal/state"
)

type fakeController struct {
	name  string
	calls int
	err   error
}

func (f *fakeController) Name() string { return f.name }

func (f *fakeController) Reconcile(_ context.Context) error {
	f.calls++
	return f.err
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestManagerRunOnceAsLeader(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	lease := controllers.NewLease(store, "leases/cm", "A", time.Minute, nil)
	mgr := controllers.NewManager(lease, time.Minute, quietLogger())
	ctrl := &fakeController{name: "demo"}
	mgr.Add(ctrl)

	leader, err := mgr.RunOnce(context.Background())
	if err != nil || !leader {
		t.Fatalf("RunOnce: leader=%v err=%v", leader, err)
	}
	if ctrl.calls != 1 {
		t.Fatalf("controller calls = %d, want 1", ctrl.calls)
	}
}

func TestManagerSkipsWhenNotLeader(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	// Another instance already holds the lease.
	other := controllers.NewLease(store, "leases/cm", "other", time.Minute, nil)
	if ok, err := other.Acquire(context.Background()); err != nil || !ok {
		t.Fatalf("seed leader: ok=%v err=%v", ok, err)
	}

	lease := controllers.NewLease(store, "leases/cm", "A", time.Minute, nil)
	mgr := controllers.NewManager(lease, time.Minute, quietLogger())
	ctrl := &fakeController{name: "demo"}
	mgr.Add(ctrl)

	leader, err := mgr.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce err: %v", err)
	}
	if leader {
		t.Fatal("should not be leader")
	}
	if ctrl.calls != 0 {
		t.Fatalf("controller should not run when not leader, calls = %d", ctrl.calls)
	}
}

func TestManagerReconcileErrorDoesNotStopLeadership(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	lease := controllers.NewLease(store, "leases/cm", "A", time.Minute, nil)
	mgr := controllers.NewManager(lease, time.Minute, quietLogger())
	mgr.Add(&fakeController{name: "bad", err: errors.New("boom")})

	leader, err := mgr.RunOnce(context.Background())
	if err != nil || !leader {
		t.Fatalf("a controller error must not fail the manager: leader=%v err=%v", leader, err)
	}
}

func TestManagerRunStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	lease := controllers.NewLease(store, "leases/cm", "A", time.Minute, nil)
	mgr := controllers.NewManager(lease, time.Hour, quietLogger())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := mgr.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
}
