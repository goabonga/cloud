// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package state

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// FileStore implements Store on the local filesystem. Keys map to file paths
// relative to baseDir, so the key "vpcs/vpc-x" is stored at
// "<baseDir>/vpcs/vpc-x". Writes are atomic (temp file + rename) and keys are
// validated so they cannot escape baseDir.
type FileStore struct {
	baseDir string
}

// NewFileStore returns a file-backed Store rooted at baseDir.
func NewFileStore(baseDir string) *FileStore {
	return &FileStore{baseDir: baseDir}
}

// resolve turns a store key into an absolute path inside baseDir, rejecting keys
// that would escape the root.
func (fs *FileStore) resolve(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("state: empty key")
	}
	clean := filepath.Clean(string(filepath.Separator) + filepath.FromSlash(key))
	rel := strings.TrimPrefix(clean, string(filepath.Separator))
	if rel == "" || rel == "." {
		return "", fmt.Errorf("state: invalid key %q", key)
	}
	return filepath.Join(fs.baseDir, rel), nil
}

// Get returns the value at key, or ErrNotFound if the file does not exist.
func (fs *FileStore) Get(key string) ([]byte, error) {
	path, err := fs.resolve(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is derived from a key validated by resolve()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("state: get %q: %w", key, err)
	}
	return data, nil
}

// Put atomically writes value at key, creating parent directories as needed.
func (fs *FileStore) Put(key string, value []byte) error {
	return fs.withWriteLock(func() error { return fs.putLocked(key, value) })
}

func (fs *FileStore) putLocked(key string, value []byte) error {
	path, err := fs.resolve(key)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("state: put mkdir %q: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("state: put temp %q: %w", key, err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(value); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("state: put write %q: %w", key, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("state: put chmod %q: %w", key, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("state: sync %q: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("state: put close %q: %w", key, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("state: put rename %q: %w", key, err)
	}
	return fs.syncDirectory(dir)
}

// Delete removes the file at key. A missing key is not an error.
func (fs *FileStore) Delete(key string) error {
	return fs.withWriteLock(func() error { return fs.deleteLocked(key) })
}

func (fs *FileStore) deleteLocked(key string) error {
	path, err := fs.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("state: delete %q: %w", key, err)
	}
	return fs.syncDirectory(filepath.Dir(path))
}

// List returns the regular files directly under prefix as key-value pairs. Keys
// are relative to baseDir and use "/" as the separator.
func (fs *FileStore) List(prefix string) ([]KeyValue, error) {
	dir, err := fs.resolve(prefix)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("state: list %q: %w", prefix, err)
	}

	kvs := make([]KeyValue, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".tmp-") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path) // #nosec G304 -- path is built from a validated prefix and a directory entry
		if err != nil {
			return nil, fmt.Errorf("state: list read %q: %w", path, err)
		}
		rel, err := filepath.Rel(fs.baseDir, path)
		if err != nil {
			return nil, fmt.Errorf("state: list rel %q: %w", path, err)
		}
		kvs = append(kvs, KeyValue{Key: filepath.ToSlash(rel), Value: data})
	}
	return kvs, nil
}

// CompareAndSwap replaces the value at key with newValue only if the current
// value equals oldValue (nil oldValue means "expect absent").
func (fs *FileStore) CompareAndSwap(key string, oldValue, newValue []byte) (bool, error) {
	var swapped bool
	err := fs.withWriteLock(func() error {
		current, err := fs.Get(key)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if (oldValue == nil && !errors.Is(err, ErrNotFound)) || !bytes.Equal(current, oldValue) {
			return nil
		}
		if err := fs.putLocked(key, newValue); err != nil {
			return err
		}
		swapped = true
		return nil
	})
	return swapped, err
}

// Close is a no-op for the file store; it satisfies the Store interface.
func (fs *FileStore) Close() error {
	return nil
}

// withWriteLock serializes every mutation across store instances and processes.
func (fs *FileStore) withWriteLock(fn func() error) error {
	if err := os.MkdirAll(fs.baseDir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(fs.baseDir, ".store.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer func() { _ = unix.Flock(int(lock.Fd()), unix.LOCK_UN) }()
	return fn()
}
func (fs *FileStore) syncDirectory(path string) error {
	rel, err := filepath.Rel(fs.baseDir, path)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(fs.baseDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	dir, err := root.Open(rel)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
