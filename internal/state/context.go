// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package state

import (
	"context"
	"time"
)

const operationTimeout = 10 * time.Second

func boundedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, operationTimeout)
}

// ContextStore supports cancellation of storage operations.
type ContextStore interface {
	GetContext(ctx context.Context, key string) ([]byte, error)
	PutContext(ctx context.Context, key string, value []byte) error
	DeleteContext(ctx context.Context, key string) error
	ListContext(ctx context.Context, prefix string) ([]KeyValue, error)
	CompareAndSwapContext(ctx context.Context, key string, oldValue, newValue []byte) (bool, error)
}

// WithContext binds a store to a request or reconciliation context.
func WithContext(ctx context.Context, store Store) Store {
	return &contextStore{Store: store, ctx: ctx}
}

type contextStore struct {
	Store
	ctx context.Context
}

func (s *contextStore) Get(key string) ([]byte, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	if backend, ok := s.Store.(ContextStore); ok {
		return backend.GetContext(s.ctx, key)
	}
	return s.Store.Get(key)
}
func (s *contextStore) Put(key string, value []byte) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if backend, ok := s.Store.(ContextStore); ok {
		return backend.PutContext(s.ctx, key, value)
	}
	return s.Store.Put(key, value)
}
func (s *contextStore) Delete(key string) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if backend, ok := s.Store.(ContextStore); ok {
		return backend.DeleteContext(s.ctx, key)
	}
	return s.Store.Delete(key)
}
func (s *contextStore) List(prefix string) ([]KeyValue, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	if backend, ok := s.Store.(ContextStore); ok {
		return backend.ListContext(s.ctx, prefix)
	}
	return s.Store.List(prefix)
}
func (s *contextStore) CompareAndSwap(key string, oldValue, newValue []byte) (bool, error) {
	if err := s.ctx.Err(); err != nil {
		return false, err
	}
	if backend, ok := s.Store.(ContextStore); ok {
		return backend.CompareAndSwapContext(s.ctx, key, oldValue, newValue)
	}
	return s.Store.CompareAndSwap(key, oldValue, newValue)
}
