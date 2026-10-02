// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package httpsec

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// MaxBodyBytes bounds API and identity request payloads, including chunked bodies.
const MaxBodyBytes = 1 << 20

const (
	requestTimeout = 2 * time.Minute
	ratePerSecond  = 100
	rateBurst      = 200
	maxPeers       = 4096
	peerIdle       = 10 * time.Minute
)

type peerBucket struct {
	tokens  float64
	updated time.Time
}
type limiter struct {
	mu    sync.Mutex
	peers map[string]peerBucket
}

func (l *limiter) allow(peer string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, exists := l.peers[peer]
	if !exists {
		if len(l.peers) >= maxPeers {
			for key, old := range l.peers {
				if now.Sub(old.updated) >= peerIdle {
					delete(l.peers, key)
				}
			}
			if len(l.peers) >= maxPeers {
				return false
			}
		}
		b = peerBucket{tokens: rateBurst, updated: now}
	}
	b.tokens = min(rateBurst, b.tokens+now.Sub(b.updated).Seconds()*ratePerSecond)
	b.updated = now
	allowed := b.tokens >= 1
	if allowed {
		b.tokens--
	}
	l.peers[peer] = b
	return allowed
}

// Limits bounds work before routing or authentication. Peer identities come from
// the connection, never untrusted forwarding headers. Instances are server-local.
func Limits(next http.Handler) http.Handler {
	l := &limiter{peers: make(map[string]peerBucket)}
	slots := make(chan struct{}, 64)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			peer = r.RemoteAddr
		}
		if !l.allow(peer, time.Now()) {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "request rate exceeded", http.StatusTooManyRequests)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			w.Header().Set("Retry-After", "1")
			http.Error(w, "server busy", http.StatusServiceUnavailable)
			return
		}
		if r.ContentLength > MaxBodyBytes {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		defer cancel()
		r = r.WithContext(ctx)
		if r.Body != nil && r.Body != http.NoBody {
			data, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
			_ = r.Body.Close()
			if len(data) > MaxBodyBytes {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			if err != nil {
				http.Error(w, "cannot read request body", http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(data))
		}
		next.ServeHTTP(w, r)
	})
}

// ConfigureServer applies connection limits to all API/identity listeners.
func ConfigureServer(s *http.Server) {
	s.ReadHeaderTimeout = 10 * time.Second
	s.ReadTimeout = 30 * time.Second
	s.WriteTimeout = requestTimeout
	s.IdleTimeout = 60 * time.Second
	s.MaxHeaderBytes = 32 << 10
}
