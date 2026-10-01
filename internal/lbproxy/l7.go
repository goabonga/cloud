// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package lbproxy

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"sync/atomic"
)

// errNoTarget reports a target group with no target to send to.
var errNoTarget = errors.New("lbproxy: no target")

// httpHandler proxies the requests of an http or https listener.
type httpHandler struct {
	l *listener
}

func (h *httpHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cfg := h.l.cfg.Load()
	name := route(cfg, r.Host, r.URL.Path)
	g := h.l.p.group(name)
	if g == nil {
		http.Error(w, "no target group", http.StatusBadGateway)
		return
	}
	serverName := ""
	if g.cfg.Protocol == ProtocolHTTPS {
		serverName = g.cfg.ServerName
		if serverName == "" {
			serverName = hostOnly(r.Host)
		}
	}
	scheme := g.cfg.Protocol
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = scheme
			// The balancing transport puts the chosen target here.
			pr.Out.URL.Host = "target"
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
		},
		Transport: &balancer{g: g, rt: g.transport(serverName)},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			h.l.p.log.Warn("proxy error", "listener", cfg.Name, "group", g.cfg.Name, "host", r.Host, "path", r.URL.Path, "err", err)
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r)
}

// balancer sends each request to a target of its group, retrying once on
// another target when the request is safe to send again.
type balancer struct {
	g  *group
	rt http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (b *balancer) RoundTrip(req *http.Request) (*http.Response, error) {
	// The transport closes a request body when it fails; a body still unread
	// is kept open, so the request can go to another target.
	var body *trackedBody
	if req.Body != nil && req.Body != http.NoBody {
		body = &trackedBody{rc: req.Body}
		defer func() { _ = body.rc.Close() }()
	}
	exclude := map[*target]bool{}
	unhealthy := b.g.cfg.HealthCheck.UnhealthyThreshold
	var lastErr error
	for attempt := 0; ; attempt++ {
		t := b.g.pick(exclude)
		if t == nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, fmt.Errorf("%w in group %q", errNoTarget, b.g.cfg.Name)
		}
		out := req.Clone(req.Context())
		out.URL.Host = t.addr()
		if body != nil {
			out.Body = body
		}
		resp, err := b.rt.RoundTrip(out)
		if err == nil {
			if resp.StatusCode >= 500 {
				t.state.observeTraffic(fmt.Errorf("answered %d", resp.StatusCode), unhealthy)
			} else {
				t.state.observeTraffic(nil, unhealthy)
			}
			return resp, nil
		}
		t.state.observeTraffic(err, unhealthy)
		lastErr = err
		if attempt >= 1 || req.Context().Err() != nil || !retryable(req.Method, body, err) {
			return nil, err
		}
		exclude[t] = true
	}
}

// retryable reports whether a failed request may go to another target: one
// whose connection could not even be established, before any byte of it was
// sent, or an idempotent one without a body consumed.
func retryable(method string, body *trackedBody, err error) bool {
	if body != nil && body.read.Load() {
		return false
	}
	if isDialError(err) {
		return true
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

// isDialError reports whether err failed to establish the connection.
func isDialError(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// trackedBody records whether a request body was read, and ignores the
// transport's Close so an unread body survives a failed attempt.
type trackedBody struct {
	rc io.ReadCloser
	// read is set by the transport's writer, which may outlive RoundTrip.
	read atomic.Bool
}

func (b *trackedBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.read.Store(true)
	}
	return n, err
}

func (b *trackedBody) Close() error { return nil }
