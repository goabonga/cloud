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

type longController struct {
	started chan struct{}
	finish  chan struct{}
}

func (*longController) Name() string { return "long" }
func (c *longController) Reconcile(ctx context.Context) error {
	close(c.started)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.finish:
		return nil
	}
}
func TestLeadershipRenewedDuringLongPass(t *testing.T) {
	store := state.NewFileStore(t.TempDir())
	lease := controllers.NewLease(store, "leases/long", "a", 150*time.Millisecond, nil)
	other := controllers.NewLease(store, "leases/long", "b", 150*time.Millisecond, nil)
	manager := controllers.NewManager(lease, time.Second, quietLogger())
	c := &longController{started: make(chan struct{}), finish: make(chan struct{})}
	manager.Add(c)
	done := make(chan error, 1)
	go func() { _, err := manager.RunOnce(context.Background()); done <- err }()
	<-c.started
	time.Sleep(400 * time.Millisecond)
	ok, err := other.Acquire(context.Background())
	close(c.finish)
	if err != nil || ok {
		t.Fatalf("other acquired during pass: %v %v", ok, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
