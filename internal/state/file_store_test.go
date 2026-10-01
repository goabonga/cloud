// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package state_test

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/goabonga/infrastructure/internal/state"
)

func TestFileStorePutGetRoundtrip(t *testing.T) {
	t.Parallel()

	fs := state.NewFileStore(t.TempDir())
	if err := fs.Put("vpcs/vpc-1", []byte("hello")); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := fs.Get("vpcs/vpc-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestFileStoreGetMissing(t *testing.T) {
	t.Parallel()

	fs := state.NewFileStore(t.TempDir())
	_, err := fs.Get("vpcs/none")
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFileStoreOverwrite(t *testing.T) {
	t.Parallel()

	fs := state.NewFileStore(t.TempDir())
	if err := fs.Put("k", []byte("v1")); err != nil {
		t.Fatalf("put v1: %v", err)
	}
	if err := fs.Put("k", []byte("v2")); err != nil {
		t.Fatalf("put v2: %v", err)
	}
	got, err := fs.Get("k")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != "v2" {
		t.Fatalf("got %q, want v2", got)
	}
}

func TestFileStoreDelete(t *testing.T) {
	t.Parallel()

	fs := state.NewFileStore(t.TempDir())
	if err := fs.Put("k", []byte("v")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := fs.Delete("k"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := fs.Get("k"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	// Deleting a missing key is not an error.
	if err := fs.Delete("k"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestFileStoreList(t *testing.T) {
	t.Parallel()

	fs := state.NewFileStore(t.TempDir())

	// Listing a non-existent prefix yields nothing, not an error.
	kvs, err := fs.List("vpcs")
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(kvs) != 0 {
		t.Fatalf("expected no entries, got %d", len(kvs))
	}

	for _, uid := range []string{"vpc-1", "vpc-2", "vpc-3"} {
		if err := fs.Put("vpcs/"+uid, []byte(uid)); err != nil {
			t.Fatalf("put %s: %v", uid, err)
		}
	}
	// A nested prefix must not appear in the parent listing.
	if err := fs.Put("vpcs/sub/vpc-x", []byte("x")); err != nil {
		t.Fatalf("put nested: %v", err)
	}

	kvs, err = fs.List("vpcs")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	keys := make([]string, 0, len(kvs))
	for _, kv := range kvs {
		keys = append(keys, kv.Key)
	}
	sort.Strings(keys)
	want := []string{"vpcs/vpc-1", "vpcs/vpc-2", "vpcs/vpc-3"}
	if len(keys) != len(want) {
		t.Fatalf("got keys %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("key[%d] = %q, want %q", i, keys[i], want[i])
		}
	}
}

func TestFileStoreCompareAndSwap(t *testing.T) {
	t.Parallel()

	fs := state.NewFileStore(t.TempDir())

	// Create only when absent (nil oldValue).
	ok, err := fs.CompareAndSwap("k", nil, []byte("v1"))
	if err != nil || !ok {
		t.Fatalf("create cas: ok=%v err=%v", ok, err)
	}

	// Stale oldValue is rejected.
	ok, err = fs.CompareAndSwap("k", []byte("stale"), []byte("v2"))
	if err != nil {
		t.Fatalf("mismatch cas err: %v", err)
	}
	if ok {
		t.Fatal("expected mismatch cas to fail")
	}

	// Correct oldValue swaps.
	ok, err = fs.CompareAndSwap("k", []byte("v1"), []byte("v2"))
	if err != nil || !ok {
		t.Fatalf("swap cas: ok=%v err=%v", ok, err)
	}
	got, _ := fs.Get("k")
	if string(got) != "v2" {
		t.Fatalf("got %q, want v2", got)
	}
}

func TestFileStoreRejectsEmptyKey(t *testing.T) {
	t.Parallel()

	fs := state.NewFileStore(t.TempDir())
	if err := fs.Put("", []byte("v")); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestFileStoreKeyCannotEscapeRoot(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	fs := state.NewFileStore(base)

	if err := fs.Put("../escape", []byte("v")); err != nil {
		t.Fatalf("put traversal key: %v", err)
	}
	// The write must land inside base, not in its parent.
	if _, err := os.Stat(filepath.Join(base, "escape")); err != nil {
		t.Fatalf("expected file inside base: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(base), "escape")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("traversal key escaped the store root")
	}
}

func TestFileStoreClose(t *testing.T) {
	t.Parallel()

	fs := state.NewFileStore(t.TempDir())
	if err := fs.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestFileStoreResolveRejectsRootKey(t *testing.T) {
	t.Parallel()

	// "/" cleans down to an empty relative path, the same invalid-key
	// branch as "" but reached through a different input.
	fs := state.NewFileStore(t.TempDir())
	if err := fs.Put("/", []byte("v")); err == nil {
		t.Fatal("expected error for a root-only key")
	}
}

func TestFileStoreGetEmptyKey(t *testing.T) {
	t.Parallel()

	fs := state.NewFileStore(t.TempDir())
	if _, err := fs.Get(""); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestFileStoreGetDirectoryError(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	fs := state.NewFileStore(base)

	if err := os.MkdirAll(filepath.Join(base, "adir"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Reading a directory as if it were a value file fails with something
	// other than ErrNotExist, so Get must wrap it rather than report
	// ErrNotFound.
	_, err := fs.Get("adir")
	if err == nil || errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected a non-ErrNotFound error, got %v", err)
	}
}

func TestFileStorePutMkdirError(t *testing.T) {
	t.Parallel()

	fs := state.NewFileStore(t.TempDir())
	if err := fs.Put("blocker", []byte("v")); err != nil {
		t.Fatalf("put blocker: %v", err)
	}
	// "blocker" is a regular file, so MkdirAll cannot create a directory
	// through it.
	if err := fs.Put("blocker/inner", []byte("v")); err == nil {
		t.Fatal("expected mkdir error when a path component is a file")
	}
}

func TestFileStorePutCreateTempError(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	fs := state.NewFileStore(base)

	if err := fs.Put("sub/first", []byte("v")); err != nil {
		t.Fatalf("put first: %v", err)
	}
	dir := filepath.Join(base, "sub")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	// MkdirAll is a no-op on the already-existing directory, so the next
	// failure is CreateTemp, which needs write permission.
	if err := fs.Put("sub/second", []byte("v")); err == nil {
		t.Fatal("expected temp file creation to fail on a read-only directory")
	}
}

func TestFileStorePutRenameError(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	fs := state.NewFileStore(base)

	if err := os.MkdirAll(filepath.Join(base, "occupied"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A directory already sits at the destination path, so the final
	// rename of the temp file onto it fails.
	if err := fs.Put("occupied", []byte("v")); err == nil {
		t.Fatal("expected rename error when the destination is a directory")
	}
}

func TestFileStoreDeleteEmptyKey(t *testing.T) {
	t.Parallel()

	fs := state.NewFileStore(t.TempDir())
	if err := fs.Delete(""); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestFileStoreDeleteError(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	fs := state.NewFileStore(base)

	if err := fs.Put("locked/leaf", []byte("v")); err != nil {
		t.Fatalf("put: %v", err)
	}
	dir := filepath.Join(base, "locked")
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	// Removing a directory entry needs write permission on its parent.
	if err := fs.Delete("locked/leaf"); err == nil {
		t.Fatal("expected delete to fail without write permission on the parent directory")
	}
}

func TestFileStoreListEmptyPrefix(t *testing.T) {
	t.Parallel()

	fs := state.NewFileStore(t.TempDir())
	if _, err := fs.List(""); err == nil {
		t.Fatal("expected error for empty prefix")
	}
}

func TestFileStoreListReadDirError(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	fs := state.NewFileStore(base)

	if err := fs.Put("locked/leaf", []byte("v")); err != nil {
		t.Fatalf("put: %v", err)
	}
	dir := filepath.Join(base, "locked")
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, err := fs.List("locked"); err == nil {
		t.Fatal("expected list to fail without permission to read the directory")
	}
}

func TestFileStoreListReadFileError(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	fs := state.NewFileStore(base)

	if err := fs.Put("locked/leaf", []byte("v")); err != nil {
		t.Fatalf("put: %v", err)
	}
	leaf := filepath.Join(base, "locked", "leaf")
	if err := os.Chmod(leaf, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(leaf, 0o600) })

	// The directory itself is still listable; reading the unreadable file
	// inside it is what must fail.
	if _, err := fs.List("locked"); err == nil {
		t.Fatal("expected list to fail reading an unreadable file")
	}
}

func TestFileStoreCompareAndSwapGetError(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	fs := state.NewFileStore(base)

	if err := os.MkdirAll(filepath.Join(base, "adir"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Get on a directory fails with something other than ErrNotFound, so
	// CompareAndSwap must surface it instead of treating the key as absent.
	if _, err := fs.CompareAndSwap("adir", nil, []byte("v")); err == nil {
		t.Fatal("expected cas to surface the underlying get error")
	}
}

func TestFileStoreCompareAndSwapPutError(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	fs := state.NewFileStore(base)

	if err := os.MkdirAll(filepath.Join(base, "parent"), 0o500); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(base, "parent"), 0o700) })

	// The key is absent, so oldValue nil matches and CompareAndSwap
	// proceeds to Put, which fails: "parent" has no write permission to
	// create the "newsub" subdirectory underneath it.
	if _, err := fs.CompareAndSwap("parent/newsub/leaf", nil, []byte("v")); err == nil {
		t.Fatal("expected cas to surface the underlying put error")
	}
}
