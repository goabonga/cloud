// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestAgentReconcileOnceAll(t *testing.T) {
	t.Parallel()

	reg := newVPCRegistry(t)
	be := newFakeBackend()
	for _, uid := range []string{"vpc-1", "vpc-2"} {
		v := &resource.VPC{Metadata: resource.ObjectMeta{UID: uid, Generation: 1}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
		if err := reg.Put(v); err != nil {
			t.Fatalf("seed %s: %v", uid, err)
		}
	}

	agent := manager.NewAgent(time.Second, quietLogger(), manager.NewVPCReconciler(reg, be))
	if err := agent.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile once: %v", err)
	}

	for _, uid := range []string{"vpc-1", "vpc-2"} {
		got, err := reg.Get(uid)
		if err != nil {
			t.Fatalf("get %s: %v", uid, err)
		}
		if !got.Status.IsReady() {
			t.Fatalf("%s phase = %q, want Ready", uid, got.Status.Phase)
		}
	}
	if len(be.bridges) != 2 {
		t.Fatalf("expected 2 bridges, got %d", len(be.bridges))
	}
}

func TestAgentReconcileOnceContinuesPastErrors(t *testing.T) {
	t.Parallel()

	reg := newVPCRegistry(t)
	be := newFakeBackend()
	be.ensureErr = errors.New("boom") // every reconcile fails at the backend
	v := &resource.VPC{Metadata: resource.ObjectMeta{UID: "vpc-1", Generation: 1}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	if err := reg.Put(v); err != nil {
		t.Fatalf("seed: %v", err)
	}

	agent := manager.NewAgent(time.Second, quietLogger(), manager.NewVPCReconciler(reg, be))
	// A per-resource failure is logged, not returned.
	if err := agent.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile once: %v", err)
	}
	got, _ := reg.Get("vpc-1")
	if got.Status.Phase != resource.PhaseError {
		t.Fatalf("phase = %q, want Error", got.Status.Phase)
	}
}

func TestAgentRunStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	agent := manager.NewAgent(time.Hour, quietLogger(), manager.NewVPCReconciler(newVPCRegistry(t), newFakeBackend()))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := agent.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v, want context.Canceled", err)
	}
}
