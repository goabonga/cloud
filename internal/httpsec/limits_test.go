// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package httpsec

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestLimitsRejectOversizedBodyBeforeHandler(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		reached := false
		handler := Limits(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(strings.Repeat("a", MaxBodyBytes+1)))
		if chunked {
			req.ContentLength = -1
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		if reached || out.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("chunked=%v status=%d handler=%v", chunked, out.Code, reached)
		}
	}
}

func TestLimiterRefillsAndBoundsPeers(t *testing.T) {
	now := time.Now()
	l := &limiter{peers: make(map[string]peerBucket)}
	for range rateBurst {
		if !l.allow("peer", now) {
			t.Fatal("burst rejected")
		}
	}
	if l.allow("peer", now) {
		t.Fatal("excess burst accepted")
	}
	if !l.allow("peer", now.Add(time.Second)) {
		t.Fatal("tokens did not refill")
	}
	for i := range maxPeers {
		l.peers[string(rune(i))] = peerBucket{updated: now}
	}
	if l.allow("new peer", now) {
		t.Fatal("unbounded peers")
	}
	if !l.allow("new peer", now.Add(peerIdle+time.Second)) {
		t.Fatal("idle peers not reclaimed")
	}
}

func TestRateCannotBeBypassedUsingForwardingHeaders(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := Limits(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
		for i := range rateBurst + 1 {
			req := httptest.NewRequest(http.MethodGet, "/token", nil)
			req.Header.Set("X-Forwarded-For", string(rune(i)))
			out := httptest.NewRecorder()
			h.ServeHTTP(out, req)
			if i == rateBurst && out.Code != http.StatusTooManyRequests {
				t.Fatalf("rate bypassed: %d", out.Code)
			}
		}
	})
}

func TestLimitsRejectConcurrentWork(t *testing.T) {
	entered := make(chan struct{}, 64)
	release := make(chan struct{})
	done := make(chan struct{}, 64)
	h := Limits(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	for range 64 {
		go func() {
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/token", nil))
			done <- struct{}{}
		}()
	}
	for range 64 {
		<-entered
	}
	out := httptest.NewRecorder()
	h.ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/token", nil))
	close(release)
	for range 64 {
		<-done
	}
	if out.Code != http.StatusServiceUnavailable {
		t.Fatalf("concurrent limit=%d", out.Code)
	}
}
