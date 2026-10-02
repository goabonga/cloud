// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore implements Store on PostgreSQL, the backend for highly available
// multi-instance deployments. Keys and values live in a single kv table;
// CompareAndSwap uses a row lock for atomicity across instances.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore connects to dsn and ensures the schema exists.
func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("state: connect postgres: %w", err)
	}
	s := &PostgresStore{pool: pool}
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY, value BYTEA NOT NULL)`); err != nil {
		pool.Close()
		return nil, fmt.Errorf("state: init schema: %w", err)
	}
	return s, nil
}

// Get returns the value at key, or ErrNotFound.
func (s *PostgresStore) Get(key string) ([]byte, error) {
	var value []byte
	err := s.pool.QueryRow(context.Background(), `SELECT value FROM kv WHERE key = $1`, key).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("state: pg get %q: %w", key, err)
	}
	return value, nil
}

// Put upserts value at key.
func (s *PostgresStore) Put(key string, value []byte) error {
	_, err := s.pool.Exec(context.Background(),
		`INSERT INTO kv (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		key, value)
	if err != nil {
		return fmt.Errorf("state: pg put %q: %w", key, err)
	}
	return nil
}

// Delete removes key. A missing key is not an error.
func (s *PostgresStore) Delete(key string) error {
	if _, err := s.pool.Exec(context.Background(), `DELETE FROM kv WHERE key = $1`, key); err != nil {
		return fmt.Errorf("state: pg delete %q: %w", key, err)
	}
	return nil
}

// List returns the key-value pairs directly under prefix (one segment deeper),
// matching the file store's single-level semantics.
func (s *PostgresStore) List(prefix string) ([]KeyValue, error) {
	rows, err := s.pool.Query(context.Background(),
		`SELECT key, value FROM kv WHERE key LIKE $1 AND key NOT LIKE $2 ORDER BY key`,
		prefix+"/%", prefix+"/%/%")
	if err != nil {
		return nil, fmt.Errorf("state: pg list %q: %w", prefix, err)
	}
	defer rows.Close()

	var kvs []KeyValue
	for rows.Next() {
		var kv KeyValue
		if err := rows.Scan(&kv.Key, &kv.Value); err != nil {
			return nil, fmt.Errorf("state: pg list scan: %w", err)
		}
		kvs = append(kvs, kv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: pg list rows: %w", err)
	}
	return kvs, nil
}

// CompareAndSwap atomically replaces the value at key with newValue only if the
// current value equals oldValue (nil oldValue means "expect absent").
func (s *PostgresStore) CompareAndSwap(key string, oldValue, newValue []byte) (bool, error) {
	ctx := context.Background()
	if oldValue == nil {
		result, err := s.pool.Exec(ctx, `INSERT INTO kv (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`, key, newValue)
		if err != nil {
			return false, fmt.Errorf("state: pg cas insert %q: %w", key, err)
		}
		return result.RowsAffected() == 1, nil
	}
	result, err := s.pool.Exec(ctx, `UPDATE kv SET value = $3 WHERE key = $1 AND value = $2`, key, oldValue, newValue)
	if err != nil {
		return false, fmt.Errorf("state: pg cas update %q: %w", key, err)
	}
	return result.RowsAffected() == 1, nil
}

// Close releases the connection pool.
func (s *PostgresStore) Close() error {
	s.pool.Close()
	return nil
}
