// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package controllers

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Controller is a single cluster-level reconcile loop.
type Controller interface {
	// Name identifies the controller in logs.
	Name() string
	// Reconcile performs one pass. It should be idempotent.
	Reconcile(ctx context.Context) error
}

// Manager runs registered controllers on an interval, but only while it holds
// the leader lease, so exactly one instance is active across the cluster.
type Manager struct {
	lease       *Lease
	interval    time.Duration
	logger      *slog.Logger
	controllers []Controller
}

// NewManager returns a Manager that competes for lease and ticks at interval.
// A nil logger uses slog.Default.
func NewManager(lease *Lease, interval time.Duration, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{lease: lease, interval: interval, logger: logger}
}

// Add registers a controller to run while this manager is the leader.
func (m *Manager) Add(c Controller) {
	m.controllers = append(m.controllers, c)
}

// RunOnce acquires (or renews) leadership and, if leader, reconciles every
// controller once. It reports whether this instance is the leader.
func (m *Manager) RunOnce(ctx context.Context) (bool, error) {
	leader, err := m.lease.Acquire(ctx)
	if err != nil {
		return false, err
	}
	if !leader {
		return false, nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopped := make(chan struct{})
	renewErr := make(chan error, 1)
	go func() {
		defer close(stopped)
		period := m.lease.ttl / 3
		if period <= 0 {
			period = time.Nanosecond
		}
		ticker := time.NewTicker(period)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				ok, err := m.lease.Renew()
				if err != nil || !ok {
					if err == nil {
						err = fmt.Errorf("controllers: leadership lost")
					}
					renewErr <- err
					cancel()
					return
				}
			}
		}
	}()
	for _, c := range m.controllers {
		if runCtx.Err() != nil {
			break
		}
		if err := c.Reconcile(runCtx); err != nil {
			m.logger.ErrorContext(runCtx, "controller reconcile", "controller", c.Name(), "err", err)
		}
	}
	cancel()
	<-stopped
	select {
	case err := <-renewErr:
		return false, err
	default:
	}
	return true, nil
}

// Run drives RunOnce on every tick until ctx is cancelled, releasing the lease
// on exit.
func (m *Manager) Run(ctx context.Context) error {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	m.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			if err := m.lease.Release(context.WithoutCancel(ctx)); err != nil {
				m.logger.Error("release lease", "err", err)
			}
			return ctx.Err()
		case <-ticker.C:
			m.tick(ctx)
		}
	}
}

func (m *Manager) tick(ctx context.Context) {
	leader, err := m.RunOnce(ctx)
	switch {
	case err != nil:
		m.logger.ErrorContext(ctx, "manager tick", "err", err)
	case !leader:
		m.logger.DebugContext(ctx, "standing by; another instance holds the lease")
	}
}
