// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package controllers runs the cluster-level reconcile loops of the
// controller-manager: leader election so a single instance is active, and a
// manager that drives registered controllers while it holds leadership.
package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/goabonga/infrastructure/internal/state"
)

// Clock returns the current time; injectable for tests.
type Clock func() time.Time

// Lease is a best-effort leader-election lock stored in a state.Store. It uses
// the store's compare-and-swap so two contenders cannot both win, and works
// over any backend (file today, etcd later).
type Lease struct {
	store  state.Store
	key    string
	holder string
	ttl    time.Duration
	now    Clock
}

type leaseRecord struct {
	Holder string    `json:"holder"`
	Expiry time.Time `json:"expiry"`
}

// NewLease returns a Lease for key, identified by holder, valid for ttl. A nil
// clock uses time.Now.
func NewLease(store state.Store, key, holder string, ttl time.Duration, clock Clock) *Lease {
	if clock == nil {
		clock = time.Now
	}
	return &Lease{store: store, key: key, holder: holder, ttl: ttl, now: clock}
}

// Acquire attempts to take the lease (when free or expired) or renew it (when
// already held by this holder). It reports whether the lease is held by this
// holder afterwards.
func (l *Lease) Acquire(_ context.Context) (bool, error) {
	if l.ttl <= 0 {
		return false, fmt.Errorf("controllers: lease TTL must be positive")
	}
	cur, err := l.store.Get(l.key)
	if errors.Is(err, state.ErrNotFound) {
		return l.swap(nil)
	}
	if err != nil {
		return false, fmt.Errorf("controllers: read lease: %w", err)
	}

	var rec leaseRecord
	if err := json.Unmarshal(cur, &rec); err != nil {
		return false, fmt.Errorf("controllers: decode lease: %w", err)
	}
	// Take it if it is ours (renew) or has expired; otherwise stand by.
	if rec.Holder == l.holder || !rec.Expiry.After(l.now()) {
		return l.swap(cur)
	}
	return false, nil
}

// swap writes a fresh record only if the stored value still equals old.
func (l *Lease) swap(old []byte) (bool, error) {
	data, err := json.Marshal(leaseRecord{Holder: l.holder, Expiry: l.now().Add(l.ttl)})
	if err != nil {
		return false, fmt.Errorf("controllers: encode lease: %w", err)
	}
	ok, err := l.store.CompareAndSwap(l.key, old, data)
	if err != nil {
		return false, fmt.Errorf("controllers: swap lease: %w", err)
	}
	return ok, nil
}

// Release gives up the lease if this holder still owns it.
func (l *Lease) Release(_ context.Context) error {
	cur, err := l.store.Get(l.key)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("controllers: read lease: %w", err)
	}
	var rec leaseRecord
	if err := json.Unmarshal(cur, &rec); err != nil {
		return fmt.Errorf("controllers: decode lease: %w", err)
	}
	if rec.Holder != l.holder {
		return nil
	}
	data, err := json.Marshal(leaseRecord{})
	if err != nil {
		return err
	}
	if _, err := l.store.CompareAndSwap(l.key, cur, data); err != nil {
		return fmt.Errorf("controllers: release lease: %w", err)
	}
	return nil
}

// Renew extends an unexpired lease only while this holder still owns it.
func (l *Lease) Renew() (bool, error) {
	cur, err := l.store.Get(l.key)
	if err != nil {
		return false, err
	}
	var rec leaseRecord
	if err := json.Unmarshal(cur, &rec); err != nil {
		return false, err
	}
	if rec.Holder != l.holder || !rec.Expiry.After(l.now()) {
		return false, nil
	}
	return l.swap(cur)
}
