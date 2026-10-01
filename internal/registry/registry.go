// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package registry is the typed layer over the byte-oriented state.Store. A
// Registry marshals and unmarshals resource.Resource envelopes of a single kind
// to and from the store, keyed by UID under a per-kind namespace.
package registry

import (
	"encoding/json"
	"fmt"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/state"
)

// Registry stores resources of one kind. S is the spec type, ST the status type.
type Registry[S any, ST any] struct {
	store state.Store
	kind  string
}

// New returns a Registry for the given kind backed by store.
func New[S any, ST any](store state.Store, kind string) *Registry[S, ST] {
	return &Registry[S, ST]{store: store, kind: kind}
}

// key returns the store key for a resource UID.
func (r *Registry[S, ST]) key(uid string) string {
	return r.kind + "/" + uid
}

// Put stores res. It stamps the envelope's APIVersion and Kind before writing.
// The resource must carry a non-empty metadata.UID.
func (r *Registry[S, ST]) Put(res *resource.Resource[S, ST]) error {
	if res == nil {
		return fmt.Errorf("registry: nil resource")
	}
	if res.Metadata.UID == "" {
		return fmt.Errorf("registry: resource has empty UID")
	}
	res.APIVersion = resource.APIVersion
	res.Kind = r.kind

	data, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("registry: marshal %s/%s: %w", r.kind, res.Metadata.UID, err)
	}
	if err := r.store.Put(r.key(res.Metadata.UID), data); err != nil {
		return fmt.Errorf("registry: put %s/%s: %w", r.kind, res.Metadata.UID, err)
	}
	return nil
}

// Get returns the resource with the given UID, or state.ErrNotFound if absent.
func (r *Registry[S, ST]) Get(uid string) (*resource.Resource[S, ST], error) {
	data, err := r.store.Get(r.key(uid))
	if err != nil {
		return nil, err
	}
	var res resource.Resource[S, ST]
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, fmt.Errorf("registry: unmarshal %s/%s: %w", r.kind, uid, err)
	}
	return &res, nil
}

// Delete removes the resource with the given UID. A missing UID is not an error.
func (r *Registry[S, ST]) Delete(uid string) error {
	if err := r.store.Delete(r.key(uid)); err != nil {
		return fmt.Errorf("registry: delete %s/%s: %w", r.kind, uid, err)
	}
	return nil
}

// TryUpdate loads the resource at uid, applies mutate to it, and writes the
// result back only if nothing else has changed the stored value since the
// load (a compare-and-swap). It reports whether the update was applied:
// false with a nil error means another writer won the race, and the caller
// should retry against a fresh read rather than treat it as a failure.
func (r *Registry[S, ST]) TryUpdate(uid string, mutate func(*resource.Resource[S, ST]) error) (bool, error) {
	key := r.key(uid)
	oldData, err := r.store.Get(key)
	if err != nil {
		return false, fmt.Errorf("registry: get %s/%s: %w", r.kind, uid, err)
	}
	var res resource.Resource[S, ST]
	if err := json.Unmarshal(oldData, &res); err != nil {
		return false, fmt.Errorf("registry: unmarshal %s/%s: %w", r.kind, uid, err)
	}
	if err := mutate(&res); err != nil {
		return false, err
	}
	res.APIVersion = resource.APIVersion
	res.Kind = r.kind
	newData, err := json.Marshal(&res)
	if err != nil {
		return false, fmt.Errorf("registry: marshal %s/%s: %w", r.kind, uid, err)
	}
	ok, err := r.store.CompareAndSwap(key, oldData, newData)
	if err != nil {
		return false, fmt.Errorf("registry: swap %s/%s: %w", r.kind, uid, err)
	}
	return ok, nil
}

// List returns every resource of this kind.
func (r *Registry[S, ST]) List() ([]resource.Resource[S, ST], error) {
	kvs, err := r.store.List(r.kind)
	if err != nil {
		return nil, fmt.Errorf("registry: list %s: %w", r.kind, err)
	}
	items := make([]resource.Resource[S, ST], 0, len(kvs))
	for _, kv := range kvs {
		var res resource.Resource[S, ST]
		if err := json.Unmarshal(kv.Value, &res); err != nil {
			return nil, fmt.Errorf("registry: unmarshal %s: %w", kv.Key, err)
		}
		items = append(items, res)
	}
	return items, nil
}
