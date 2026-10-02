// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package replication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestResumeUsesImmutableSnapshotAfterLiveDiskChanges(t *testing.T) {
	dir := t.TempDir()
	key := bytes.Repeat([]byte("k"), 32)
	original := []byte("old disk contents")
	path := filepath.Join(dir, "disk-1.img")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	srv := NewFixtureServer(dir, key, "primary")
	snapshot, digest, err := srv.openSnapshot("disk-1", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = snapshot.Close()
	if err := os.WriteFile(path, []byte("new disk contents"), 0600); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	dest := filepath.Join(t.TempDir(), "replica.img")
	if err := os.WriteFile(dest+".part", original[:5], 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+".part.version", []byte(digest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := NewClient(key, "secondary").PullDisk(context.Background(), ts.Listener.Addr().String(), "disk-1", dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("mixed disk versions: %s", got)
	}
}

func TestUnsupportedSnapshotDoesNotCopyLiveDisk(t *testing.T) {
	dir := t.TempDir()
	key := bytes.Repeat([]byte("k"), 32)
	if err := os.WriteFile(filepath.Join(dir, "disk-1.img"), []byte("live"), 0600); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(dir, key, "primary", nil)
	srv.clone = func(*os.File, *os.File) error { return unix.EOPNOTSUPP }
	req := httptest.NewRequest(http.MethodGet, "/disks/disk-1", nil)
	signRequest(req, key, "secondary")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unsafe fallback: %d", w.Code)
	}
	files, err := os.ReadDir(filepath.Join(dir, ".snapshots", "disk-1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatal("failed capture left partial snapshot")
	}
}

func TestCorruptPartialCannotReplaceExistingReplica(t *testing.T) {
	dir := t.TempDir()
	key := bytes.Repeat([]byte("k"), 32)
	data := []byte("correct disk")
	if err := os.WriteFile(filepath.Join(dir, "disk-1.img"), data, 0600); err != nil {
		t.Fatal(err)
	}
	srv := NewFixtureServer(dir, key, "primary")
	snapshot, digest, err := srv.openSnapshot("disk-1", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = snapshot.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	dest := filepath.Join(t.TempDir(), "replica.img")
	for path, data := range map[string][]byte{dest: []byte("existing replica"), dest + ".part": []byte("wrong"), dest + ".part.version": []byte(digest)} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	client := NewClient(key, "secondary")
	if err := client.PullDisk(context.Background(), ts.Listener.Addr().String(), "disk-1", dest); err == nil {
		t.Fatal("corruption accepted")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "existing replica" {
		t.Fatal("old replica replaced with corrupted data")
	}
	if err := client.PullDisk(context.Background(), ts.Listener.Addr().String(), "disk-1", dest); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("retry failed to replace corrupted partial")
	}
}

func TestCompletedPartialRestartsAfterRangeNotSatisfiable(t *testing.T) {
	data := []byte("complete")
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("X-Infra-SHA256", digest)
		_, _ = w.Write(data)
	}))
	defer ts.Close()
	dest := filepath.Join(t.TempDir(), "disk.img")
	if err := os.WriteFile(dest+".part", data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+".part.version", []byte(digest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := NewClient([]byte("key"), "node").PullDisk(context.Background(), ts.Listener.Addr().String(), "disk-1", dest); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotEvictionRequiresFreshTransfer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "disk-1.img")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	srv := NewFixtureServer(dir, []byte("key"), "node")
	old, digest, err := srv.openSnapshot("disk-1", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = old.Close() }()
	if err := os.WriteFile(path, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	latest, _, err := srv.openSnapshot("disk-1", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = latest.Close()
	if _, _, err := srv.openSnapshot("disk-1", digest); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old snapshot still reusable: %v", err)
	}
	got, err := io.ReadAll(old)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatal("active snapshot reader changed")
	}
}

func TestFilesystemSnapshotIsAtomicOrFailsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "disk-1.img")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(dir, []byte("key"), "node", nil)
	snapshot, _, err := srv.openSnapshot("disk-1", "")
	if err != nil {
		if !errors.Is(err, unix.EOPNOTSUPP) && !errors.Is(err, unix.EXDEV) && !errors.Is(err, unix.ENOTTY) {
			t.Fatal(err)
		}
		files, readErr := os.ReadDir(filepath.Join(dir, ".snapshots", "disk-1"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if len(files) != 0 {
			t.Fatal("unsupported filesystem left a snapshot")
		}
		return
	}
	defer func() { _ = snapshot.Close() }()
	if err := os.WriteFile(path, []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatal("reflink content changed with live disk")
	}
}
