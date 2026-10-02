// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package state

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/goabonga/infrastructure/internal/transporttls"
)

// EtcdStore implements Store on etcd v3, a highly available multi-instance
// backend. Keys map directly to etcd keys; CompareAndSwap is a single etcd
// transaction, so it is atomic across instances without an external lock.
type EtcdStore struct {
	client *clientv3.Client
}

// NewEtcdStore connects to the given etcd endpoints (host:port). The client
// dials lazily, so this returns without a live connection.
func NewEtcdStore(endpoints []string) (*EtcdStore, error) {
	config, err := transporttls.FromEnvironment()
	if err != nil {
		return nil, err
	}
	normalized := make([]string, len(endpoints))
	for i, endpoint := range endpoints {
		raw := endpoint
		if !strings.Contains(raw, "://") {
			raw = "http://" + raw
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, fmt.Errorf("state: invalid etcd endpoint")
		}
		if config == nil {
			host := parsed.Hostname()
			ip := net.ParseIP(host)
			if parsed.Scheme == "https" || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
				return nil, fmt.Errorf("state: remote etcd requires management TLS credentials")
			}
		} else {
			if strings.HasPrefix(endpoint, "http://") {
				return nil, fmt.Errorf("state: plaintext etcd endpoint is incompatible with management TLS")
			}
			parsed.Scheme = "https"
		}
		normalized[i] = parsed.String()
	}
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   normalized,
		TLS:         config,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("state: connect etcd: %w", err)
	}
	return &EtcdStore{client: client}, nil
}

// GetContext returns the value at key, or ErrNotFound.
func (s *EtcdStore) GetContext(ctx context.Context, key string) ([]byte, error) {
	ctx, cancel := boundedContext(ctx)
	defer cancel()
	resp, err := s.client.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("state: etcd get %q: %w", key, err)
	}
	if len(resp.Kvs) == 0 {
		return nil, ErrNotFound
	}
	return resp.Kvs[0].Value, nil
}

// PutContext stores value at key.
func (s *EtcdStore) PutContext(ctx context.Context, key string, value []byte) error {
	ctx, cancel := boundedContext(ctx)
	defer cancel()
	if _, err := s.client.Put(ctx, key, string(value)); err != nil {
		return fmt.Errorf("state: etcd put %q: %w", key, err)
	}
	return nil
}

// DeleteContext removes key. A missing key is not an error.
func (s *EtcdStore) DeleteContext(ctx context.Context, key string) error {
	ctx, cancel := boundedContext(ctx)
	defer cancel()
	if _, err := s.client.Delete(ctx, key); err != nil {
		return fmt.Errorf("state: etcd delete %q: %w", key, err)
	}
	return nil
}

// ListContext returns the key-value pairs directly under prefix (one segment deeper),
// matching the file store's single-level semantics.
func (s *EtcdStore) ListContext(ctx context.Context, prefix string) ([]KeyValue, error) {
	ctx, cancel := boundedContext(ctx)
	defer cancel()
	scan := prefix + "/"
	resp, err := s.client.Get(ctx, scan,
		clientv3.WithPrefix(),
		clientv3.WithSort(clientv3.SortByKey, clientv3.SortAscend))
	if err != nil {
		return nil, fmt.Errorf("state: etcd list %q: %w", prefix, err)
	}
	var kvs []KeyValue
	for _, kv := range resp.Kvs {
		// Keep only direct children: the remainder after prefix+"/" must not
		// contain a further separator.
		if strings.Contains(string(kv.Key)[len(scan):], "/") {
			continue
		}
		kvs = append(kvs, KeyValue{Key: string(kv.Key), Value: kv.Value})
	}
	return kvs, nil
}

// CompareAndSwapContext atomically replaces the value at key with newValue only if the
// current value equals oldValue (nil oldValue means "expect absent").
func (s *EtcdStore) CompareAndSwapContext(ctx context.Context, key string, oldValue, newValue []byte) (bool, error) {
	ctx, cancel := boundedContext(ctx)
	defer cancel()
	var cmp clientv3.Cmp
	if oldValue == nil {
		// Expect absent: a key that has never been created has revision 0.
		cmp = clientv3.Compare(clientv3.CreateRevision(key), "=", 0)
	} else {
		cmp = clientv3.Compare(clientv3.Value(key), "=", string(oldValue))
	}
	resp, err := s.client.Txn(ctx).
		If(cmp).
		Then(clientv3.OpPut(key, string(newValue))).
		Commit()
	if err != nil {
		return false, fmt.Errorf("state: etcd cas %q: %w", key, err)
	}
	return resp.Succeeded, nil
}

// Close releases the etcd client.
func (s *EtcdStore) Close() error {
	if err := s.client.Close(); err != nil {
		return fmt.Errorf("state: etcd close: %w", err)
	}
	return nil
}

// Get uses a bounded default context.
func (s *EtcdStore) Get(key string) ([]byte, error) { return s.GetContext(context.Background(), key) }

// Put uses a bounded default context.
func (s *EtcdStore) Put(key string, value []byte) error {
	return s.PutContext(context.Background(), key, value)
}

// Delete uses a bounded default context.
func (s *EtcdStore) Delete(key string) error { return s.DeleteContext(context.Background(), key) }

// List uses a bounded default context.
func (s *EtcdStore) List(prefix string) ([]KeyValue, error) {
	return s.ListContext(context.Background(), prefix)
}

// CompareAndSwap uses a bounded default context.
func (s *EtcdStore) CompareAndSwap(key string, oldValue, newValue []byte) (bool, error) {
	return s.CompareAndSwapContext(context.Background(), key, oldValue, newValue)
}
