// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package lbproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// helloTimeout bounds how long a tls listener waits for a ClientHello.
const helloTimeout = 10 * time.Second

// acceptLoop serves a tcp or tls listener until its socket closes.
func (l *listener) acceptLoop(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if !l.track(c) {
			_ = c.Close()
			continue
		}
		go func() {
			defer l.untrack(c)
			defer func() { _ = c.Close() }()
			l.serveConn(c)
		}()
	}
}

// serveConn sends a connection to a target: the default group's for tcp, the
// group of the rule matching its SNI for tls passthrough. The TLS session is
// never terminated: the ClientHello read to route it is replayed.
func (l *listener) serveConn(c net.Conn) {
	cfg := l.cfg.Load()
	var prefix []byte
	name := cfg.DefaultTargetGroup
	if cfg.Protocol == ProtocolTLS {
		_ = c.SetReadDeadline(time.Now().Add(helloTimeout))
		sni, hello, err := peekSNI(c)
		_ = c.SetReadDeadline(time.Time{})
		if err != nil {
			l.p.log.Debug("no ClientHello", "listener", cfg.Name, "err", err)
			return
		}
		prefix = hello
		name = route(cfg, sni, "")
	}
	g := l.p.group(name)
	if g == nil {
		return
	}
	backend, err := dialTarget(g)
	if err != nil {
		l.p.log.Warn("no target reachable", "listener", cfg.Name, "group", name, "err", err)
		return
	}
	defer func() { _ = backend.Close() }()
	if len(prefix) > 0 {
		if _, err := backend.Write(prefix); err != nil {
			return
		}
	}
	splice(c, backend)
}

// dialTarget connects to a target of g, trying a second one when the first
// cannot be reached.
func dialTarget(g *group) (net.Conn, error) {
	exclude := map[*target]bool{}
	unhealthy := g.cfg.HealthCheck.UnhealthyThreshold
	lastErr := errNoTarget
	for range 2 {
		t := g.pick(exclude)
		if t == nil {
			break
		}
		conn, err := dialer.DialContext(context.Background(), "tcp", t.addr())
		t.state.observeTraffic(err, unhealthy)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		exclude[t] = true
	}
	return nil, lastErr
}

// splice copies both ways until both sides are done, half-closing each
// direction as it ends.
func splice(a, b net.Conn) {
	var wg sync.WaitGroup
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.Close()
		}
	}
	wg.Add(2)
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
}

// errHelloRead stops the handshake once the ClientHello is read.
var errHelloRead = errors.New("lbproxy: client hello read")

// peekSNI reads the ClientHello on c and returns its server name and the
// bytes read, to be replayed to the target. It answers nothing to the client.
func peekSNI(c net.Conn) (string, []byte, error) {
	var buf bytes.Buffer
	var sni string
	seen := false
	err := tls.Server(readOnlyConn{Conn: c, r: io.TeeReader(c, &buf)}, &tls.Config{
		GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			sni, seen = h.ServerName, true
			return nil, errHelloRead
		},
	}).Handshake()
	if !seen {
		return "", nil, err
	}
	return sni, buf.Bytes(), nil
}

// readOnlyConn reads through r and swallows writes, so a handshake started
// only to read the ClientHello sends nothing back.
type readOnlyConn struct {
	net.Conn
	r io.Reader
}

func (c readOnlyConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c readOnlyConn) Write(p []byte) (int, error) { return len(p), nil }
