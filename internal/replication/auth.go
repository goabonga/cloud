// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package replication is the agent-to-agent transport disk replication is
// built on: a disk's backing file pulled from the node that owns it, and a
// reachability probe used by failover to double-check a stale heartbeat
// before promoting a replica. There is no other node-to-node channel in the
// codebase to reuse; this is the first one.
package replication

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"
)

// authWindow bounds the clock skew (and so the replay window) a signed
// request is accepted within.
const authWindow = 5 * time.Minute

const (
	headerNode      = "X-Infra-Node"
	headerTimestamp = "X-Infra-Timestamp"
	headerSignature = "X-Infra-Signature"
)

// sign computes the hex-encoded HMAC-SHA256 over method, path and ts, keyed
// by key. Both client and server compute the same value from the same
// request line; nothing else is covered (the body is never covered since
// every request here is a GET with no body).
func sign(key []byte, method, path string, ts time.Time) string {
	mac := hmac.New(sha256.New, key)
	// hash.Hash.Write never returns an error; nothing to check.
	_, _ = fmt.Fprintf(mac, "%s\n%s\n%s", method, path, ts.UTC().Format(time.RFC3339))
	return hex.EncodeToString(mac.Sum(nil))
}

// signRequest adds the node, timestamp and signature headers to req.
func signRequest(req *http.Request, key []byte, nodeID string) {
	ts := time.Now()
	req.Header.Set(headerNode, nodeID)
	req.Header.Set(headerTimestamp, ts.UTC().Format(time.RFC3339))
	req.Header.Set(headerSignature, sign(key, req.Method, req.URL.Path, ts))
}

// verifyRequest reports whether r carries a timestamp within authWindow of
// now and a signature matching key for its method, path and timestamp.
func verifyRequest(r *http.Request, key []byte, now time.Time) bool {
	tsRaw := r.Header.Get(headerTimestamp)
	ts, err := time.Parse(time.RFC3339, tsRaw)
	if err != nil {
		return false
	}
	skew := now.Sub(ts)
	if skew < 0 {
		skew = -skew
	}
	if skew > authWindow {
		return false
	}
	want := sign(key, r.Method, r.URL.Path, ts)
	got := r.Header.Get(headerSignature)
	return hmac.Equal([]byte(want), []byte(got))
}
