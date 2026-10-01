// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package replication

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestVerifyRequestAcceptsAFreshSignature(t *testing.T) {
	t.Parallel()

	key := []byte("a-shared-key")
	req := httptest.NewRequest("GET", "/disks/disk-1", nil)
	signRequest(req, key, "node-a")

	if !verifyRequest(req, key, time.Now()) {
		t.Fatal("expected a freshly signed request to verify")
	}
}

func TestVerifyRequestRejectsAnExpiredTimestamp(t *testing.T) {
	t.Parallel()

	key := []byte("a-shared-key")
	req := httptest.NewRequest("GET", "/disks/disk-1", nil)
	signRequest(req, key, "node-a")

	future := time.Now().Add(authWindow + time.Minute)
	if verifyRequest(req, key, future) {
		t.Fatal("expected a stale timestamp to be rejected")
	}
}

func TestVerifyRequestRejectsATamperedPath(t *testing.T) {
	t.Parallel()

	key := []byte("a-shared-key")
	req := httptest.NewRequest("GET", "/disks/disk-1", nil)
	signRequest(req, key, "node-a")
	req.URL.Path = "/disks/disk-2" // signed for disk-1, now pointed at disk-2

	if verifyRequest(req, key, time.Now()) {
		t.Fatal("expected a path changed after signing to fail verification")
	}
}
