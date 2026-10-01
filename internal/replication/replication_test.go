// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package replication_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/goabonga/infrastructure/internal/replication"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

// newTestServer seeds dir/uid.img with content and starts an httptest server
// in front of a replication.Server rooted at dir.
func newTestServer(t *testing.T, key []byte, uid string, content []byte) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, uid+".img"), content, 0o600); err != nil {
		t.Fatalf("seed disk file: %v", err)
	}
	srv := replication.NewServer(dir, key, "node-primary", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts.Listener.Addr().String()
}

func TestPing(t *testing.T) {
	t.Parallel()

	key := testKey(t)
	addr := newTestServer(t, key, "disk-1", []byte("irrelevant"))
	client := replication.NewClient(key, "node-secondary")

	if err := client.Ping(context.Background(), addr); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestPullDiskFullTransfer(t *testing.T) {
	t.Parallel()

	key := testKey(t)
	content := bytes.Repeat([]byte("abcdefgh"), 4096) // 32KiB, bigger than a single read
	addr := newTestServer(t, key, "disk-1", content)
	client := replication.NewClient(key, "node-secondary")

	dest := filepath.Join(t.TempDir(), "disk-1.img")
	if err := client.PullDisk(context.Background(), addr, "disk-1", dest); err != nil {
		t.Fatalf("pull: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("content mismatch: got %d bytes, want %d", len(got), len(content))
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatalf("expected .part to be gone after a complete transfer, err=%v", err)
	}
}

func TestPullDiskResumesFromPartial(t *testing.T) {
	t.Parallel()

	key := testKey(t)
	content := bytes.Repeat([]byte("0123456789"), 1000) // 10,000 bytes
	addr := newTestServer(t, key, "disk-1", content)
	client := replication.NewClient(key, "node-secondary")

	dest := filepath.Join(t.TempDir(), "disk-1.img")
	// Simulate a prior attempt that only got the first half.
	if err := os.WriteFile(dest+".part", content[:5000], 0o600); err != nil {
		t.Fatalf("seed partial: %v", err)
	}

	if err := client.PullDisk(context.Background(), addr, "disk-1", dest); err != nil {
		t.Fatalf("pull: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatal("resumed content does not match the full file")
	}
}

func TestPullDiskRejectsWrongKey(t *testing.T) {
	t.Parallel()

	addr := newTestServer(t, testKey(t), "disk-1", []byte("secret bytes"))
	client := replication.NewClient(testKey(t), "node-secondary") // different key

	dest := filepath.Join(t.TempDir(), "disk-1.img")
	if err := client.PullDisk(context.Background(), addr, "disk-1", dest); err == nil {
		t.Fatal("expected an error pulling with the wrong key")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("no file should have been written for a rejected pull")
	}
}

func TestPullDiskRejectsInvalidUID(t *testing.T) {
	t.Parallel()

	key := testKey(t)
	addr := newTestServer(t, key, "disk-1", []byte("x"))
	client := replication.NewClient(key, "node-secondary")

	dest := filepath.Join(t.TempDir(), "disk-1.img")
	// A URL-safe single path segment containing a character validUID
	// rejects (a segment with "/" would never even match
	// "GET /disks/{uid}", so this exercises validUID itself).
	if err := client.PullDisk(context.Background(), addr, "disk.1", dest); err == nil {
		t.Fatal("expected the server to reject a non-UID-shaped path segment")
	}
}

func TestPullDiskMissingDisk(t *testing.T) {
	t.Parallel()

	key := testKey(t)
	addr := newTestServer(t, key, "disk-1", []byte("x"))
	client := replication.NewClient(key, "node-secondary")

	dest := filepath.Join(t.TempDir(), "disk-2.img")
	if err := client.PullDisk(context.Background(), addr, "disk-2", dest); err == nil {
		t.Fatal("expected an error pulling a disk the server does not have")
	}
}
