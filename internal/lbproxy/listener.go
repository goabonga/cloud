// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package lbproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// dialer reaches the targets.
var dialer = &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}

func itoa(i int) string { return strconv.Itoa(i) }

// bindKey identifies what a listener's socket is bound for: a listener whose
// key is unchanged by a reload keeps its socket.
func bindKey(l Listener) string {
	return l.Address + "|" + itoa(l.Port) + "|" + l.Protocol + "|" + l.TLSMode
}

// listenConfig binds with SO_REUSEPORT, so a restarted infra-lb can bind
// before the old one lets go, and IP_FREEBIND, so a listener can bind an
// address before it is assigned to the namespace.
var listenConfig = net.ListenConfig{
	Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			s := int(fd) // #nosec G115 -- a file descriptor fits an int
			if serr = unix.SetsockoptInt(s, unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); serr != nil {
				return
			}
			if serr = unix.SetsockoptInt(s, unix.SOL_SOCKET, unix.SO_REUSEPORT, 1); serr != nil {
				return
			}
			// Best effort: binding an assigned address does not need it.
			_ = unix.SetsockoptInt(s, unix.IPPROTO_IP, unix.IP_FREEBIND, 1)
		})
		if err != nil {
			return err
		}
		return serr
	},
}

// certSet is the certificates an https listener serves.
type certSet struct {
	certs []tls.Certificate
}

func newCertSet(certs []Certificate) *certSet {
	s := &certSet{}
	for _, c := range certs {
		// Validated before Apply: a pair that does not load is skipped.
		pair, err := tls.X509KeyPair([]byte(c.CertPEM), []byte(c.KeyPEM))
		if err != nil {
			continue
		}
		if pair.Leaf == nil && len(pair.Certificate) > 0 {
			pair.Leaf, _ = x509.ParseCertificate(pair.Certificate[0])
		}
		s.certs = append(s.certs, pair)
	}
	return s
}

// pick returns the certificate whose leaf names serverName, else the first.
func (s *certSet) pick(serverName string) *tls.Certificate {
	name := strings.ToLower(serverName)
	for i := range s.certs {
		if leaf := s.certs[i].Leaf; leaf != nil && name != "" {
			for _, dns := range leaf.DNSNames {
				if matchHost(dns, name) {
					return &s.certs[i]
				}
			}
		}
	}
	if len(s.certs) == 0 {
		return nil
	}
	return &s.certs[0]
}

// matchHost reports whether host matches pattern: exactly, or below the
// suffix of a "*.example.com" pattern, case-insensitively.
func matchHost(pattern, host string) bool {
	pattern, host = strings.ToLower(pattern), strings.ToLower(host)
	if suffix, ok := strings.CutPrefix(pattern, "*"); ok && strings.HasPrefix(suffix, ".") {
		return len(host) > len(suffix) && strings.HasSuffix(host, suffix)
	}
	return pattern == host
}

// hostOnly strips the port from a Host header.
func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return strings.Trim(hostport, "[]")
}

// route returns the target group of a request to host and path: the rule
// with the longest matching path prefix, a rule naming the host winning a
// tie, else the default group.
func route(l *Listener, host, path string) string {
	host = strings.ToLower(hostOnly(host))
	best, bestScore := l.DefaultTargetGroup, -1
	for _, r := range l.Rules {
		if r.Host != "" && !matchHost(r.Host, host) {
			continue
		}
		if r.PathPrefix != "" && !strings.HasPrefix(path, r.PathPrefix) {
			continue
		}
		score := 2 * len(r.PathPrefix)
		if r.Host != "" {
			score++
		}
		if score > bestScore {
			best, bestScore = r.TargetGroup, score
		}
	}
	return best
}

// listener is one bound socket and what it serves.
type listener struct {
	p     *Proxy
	key   string
	cfg   atomic.Pointer[Listener]
	certs atomic.Pointer[certSet]

	mu      sync.Mutex
	ln      net.Listener
	srv     *http.Server
	bindErr error
	closed  bool
	conns   map[net.Conn]struct{}
	active  sync.WaitGroup
}

func newListener(p *Proxy, cfg Listener) *listener {
	l := &listener{p: p, key: bindKey(cfg), conns: map[net.Conn]struct{}{}}
	l.update(cfg)
	return l
}

func (l *listener) bindKey() string { return l.key }

// update swaps the listener's routes and certificates in.
func (l *listener) update(cfg Listener) {
	c := cfg
	l.cfg.Store(&c)
	l.certs.Store(newCertSet(cfg.Certificates))
}

func (l *listener) bound() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ln != nil
}

func (l *listener) status() (bool, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.bindErr != nil {
		return l.ln != nil, l.bindErr.Error()
	}
	return l.ln != nil, ""
}

// bind opens the socket and starts serving it. A failure is kept for the
// status and retried by RetryBinds.
func (l *listener) bind() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ln != nil || l.closed {
		return
	}
	cfg := l.cfg.Load()
	network := "tcp"
	if ip := net.ParseIP(cfg.Address); ip != nil && ip.To4() != nil {
		network = "tcp4"
	}
	ln, err := listenConfig.Listen(context.Background(), network, net.JoinHostPort(cfg.Address, itoa(cfg.Port)))
	if err != nil {
		if l.bindErr == nil || l.bindErr.Error() != err.Error() {
			l.p.log.Warn("listener not bound", "listener", cfg.Name, "err", err)
		}
		l.bindErr = err
		return
	}
	l.bindErr = nil
	l.ln = ln
	switch cfg.Protocol {
	case ProtocolHTTP, ProtocolHTTPS:
		if cfg.Protocol == ProtocolHTTPS {
			ln = tls.NewListener(ln, &tls.Config{
				MinVersion: tls.VersionTLS12,
				GetCertificate: func(h *tls.ClientHelloInfo) (*tls.Certificate, error) {
					if c := l.certs.Load().pick(h.ServerName); c != nil {
						return c, nil
					}
					return nil, errors.New("lbproxy: no certificate")
				},
			})
		}
		l.srv = &http.Server{
			Handler:           &httpHandler{l: l},
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
			ErrorLog:          slog.NewLogLogger(l.p.log.Handler(), slog.LevelDebug),
		}
		srv := l.srv
		go func() { _ = srv.Serve(ln) }()
	default:
		go l.acceptLoop(ln)
	}
	l.p.log.Info("listener bound", "listener", cfg.Name, "address", ln.Addr().String(), "protocol", cfg.Protocol)
}

// close stops accepting at once, then waits up to drain for in-flight
// requests and connections before cutting them.
func (l *listener) close(drain time.Duration) {
	l.mu.Lock()
	l.closed = true
	ln, srv := l.ln, l.srv
	l.mu.Unlock()
	if ln == nil {
		return
	}
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), drain)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			_ = srv.Close()
		}
		return
	}
	_ = ln.Close()
	done := make(chan struct{})
	go func() { l.active.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(drain):
		l.mu.Lock()
		for c := range l.conns {
			_ = c.Close()
		}
		l.mu.Unlock()
		<-done
	}
}

// track registers a connection being served, unless the listener closes.
func (l *listener) track(c net.Conn) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return false
	}
	l.conns[c] = struct{}{}
	l.active.Add(1)
	return true
}

func (l *listener) untrack(c net.Conn) {
	l.mu.Lock()
	delete(l.conns, c)
	l.mu.Unlock()
	l.active.Done()
}
