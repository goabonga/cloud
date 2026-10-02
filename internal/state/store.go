// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package state provides the byte-oriented key-value store that backs the
// declarative resource model. It knows nothing about the resource schema; the
// typed layer lives in internal/registry.
package state

import "errors"

// ErrNotFound is returned by Get when a key is absent. Callers should test for
// it with errors.Is rather than comparing returned bytes against nil.
var ErrNotFound = errors.New("state: key not found")

// ErrConflict reports an attempted write against a stale revision.
var ErrConflict = errors.New("state: resource version conflict")

// Store is the storage backend abstraction. Implementations: FileStore
// (single-host, local filesystem) and, later, an etcd-backed store.
type Store interface {
	// Get returns the value stored at key, or ErrNotFound if it is absent.
	Get(key string) ([]byte, error)

	// Put stores value at key, creating any parent namespaces as needed.
	Put(key string, value []byte) error

	// Delete removes key. Deleting a missing key is not an error.
	Delete(key string) error

	// List returns every key-value pair directly under prefix. Keys are
	// returned relative to the store root, using "/" as the separator.
	List(prefix string) ([]KeyValue, error)

	// CompareAndSwap atomically replaces the value at key with newValue only if
	// the current value equals oldValue (a nil oldValue means "expect absent").
	// It reports whether the swap was applied.
	CompareAndSwap(key string, oldValue, newValue []byte) (bool, error)

	// Close releases any resources held by the store.
	Close() error
}

// KeyValue is a single entry returned by List.
type KeyValue struct {
	Key   string
	Value []byte
}
