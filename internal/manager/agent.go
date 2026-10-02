// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"log/slog"
	"time"
)

// ReconcilePass reconciles every resource of one kind. Implementations:
// VPCReconciler, ACLReconciler.
type ReconcilePass interface {
	// Name identifies the pass in logs.
	Name() string
	// ReconcileAll reconciles every resource of the kind once.
	ReconcileAll(ctx context.Context) error
}

// Agent runs a set of reconcile passes on a fixed interval. A failing pass is
// logged and the others still run.
type Agent struct {
	passes   []ReconcilePass
	interval time.Duration
	logger   *slog.Logger
}

// NewAgent wires an Agent over the given passes. A nil logger uses slog.Default.
func NewAgent(interval time.Duration, logger *slog.Logger, passes ...ReconcilePass) *Agent {
	if logger == nil {
		logger = slog.Default()
	}
	return &Agent{passes: passes, interval: interval, logger: logger}
}

// ReconcileOnce runs every pass once.
func (a *Agent) ReconcileOnce(ctx context.Context) error {
	for _, p := range a.passes {
		if err := p.ReconcileAll(ctx); err != nil {
			a.logger.ErrorContext(ctx, "reconcile pass", "pass", p.Name(), "err", err)
		}
	}
	return nil
}

// Run reconciles immediately and then on every tick until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	a.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			a.tick(ctx)
		}
	}
}

func (a *Agent) tick(ctx context.Context) {
	if err := a.ReconcileOnce(ctx); err != nil {
		a.logger.ErrorContext(ctx, "reconcile tick", "err", err)
	}
}
