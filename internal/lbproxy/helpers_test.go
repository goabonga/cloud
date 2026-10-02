// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package lbproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/goabonga/infrastructure/internal/pki"
)

// testLogger discards the proxy's logs.
func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// startProxy applies cfg to a proxy whose health "seconds" last 10ms.
func startProxy(t *testing.T, cfg *Config) *Proxy {
	t.Helper()
	p := New(testLogger())
	p.unit = 10 * time.Millisecond
	p.drain = time.Second
	cfg.setDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}
	p.Apply(cfg)
	t.Cleanup(p.Shutdown)
	return p
}

// freePort reserves a TCP port on 127.0.0.1 for the test: a socket bound to
// it with SO_REUSEPORT, never listening, held until the test ends. No other
// socket gets the port in the meantime, while a listener with SO_REUSEPORT,
// as the proxy's are, can still bind it - and a socket that does not listen
// takes none of its connections.
func freePort(t *testing.T) int {
	t.Helper()
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(fd) })
	if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_REUSEPORT, 1); err != nil {
		t.Fatal(err)
	}
	if err := unix.Bind(fd, &unix.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		t.Fatal(err)
	}
	sa, err := unix.Getsockname(fd)
	if err != nil {
		t.Fatal(err)
	}
	return sa.(*unix.SockaddrInet4).Port
}

// target returns the Target of a test server.
func targetOf(t *testing.T, rawURL string) Target {
	t.Helper()
	hp := strings.TrimPrefix(strings.TrimPrefix(rawURL, "http://"), "https://")
	host, port, err := net.SplitHostPort(hp)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(port)
	return Target{Address: host, Port: p}
}

// named answers every request with its name, and 200 on /healthz.
func named(t *testing.T, name string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, name)
	}))
	t.Cleanup(s.Close)
	return s
}

// newCA returns a fresh test CA.
func newCA(t *testing.T, cn string) *pki.CA {
	t.Helper()
	ca, err := pki.NewCA(pki.CASpec{CommonName: cn, ValidFor: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

// issue signs a certificate for names with ca.
func issue(t *testing.T, ca *pki.CA, names ...string) Certificate {
	t.Helper()
	cert, key, err := ca.Issue(pki.CertSpec{CommonName: names[0], DNSNames: names, ValidFor: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	return Certificate{CertPEM: string(cert), KeyPEM: string(key)}
}

// tlsServer starts an HTTPS test server answering name, with c.
func tlsServer(t *testing.T, name string, c Certificate) *httptest.Server {
	t.Helper()
	pair, err := tls.X509KeyPair([]byte(c.CertPEM), []byte(c.KeyPEM))
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, name)
	}))
	s.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

// clientTrusting returns an HTTP client verifying servers against ca, and
// dialing every name to addr.
func clientTrusting(ca *pki.CA, addr string) *http.Client {
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CertPEM)
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}
}

// get fetches url with c and returns status and body.
func get(t *testing.T, c *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// eventually polls cond for up to 10s.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// health returns the health of the target at address:port of group.
func health(p *Proxy, group string, tg Target) string {
	for _, g := range p.Status().TargetGroups {
		if g.Name != group {
			continue
		}
		for _, ts := range g.Targets {
			if ts.Address == tg.Address && ts.Port == tg.Port {
				return ts.Health
			}
		}
	}
	return ""
}

func addr(port int) string { return "127.0.0.1:" + strconv.Itoa(port) }

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }

// waitStatus polls the status file at path until cond holds, reporting the
// last status read when it never does.
func waitStatus(t *testing.T, path, what string, cond func(Status) bool) {
	t.Helper()
	var last Status
	var lastErr error
	deadline := time.Now().Add(10 * time.Second)
	for {
		last, lastErr = readStatus(path)
		if lastErr == nil && cond(last) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s: last status %+v, err %v", what, last, lastErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// readStatus reads a status file.
func readStatus(path string) (Status, error) {
	var st Status
	raw, err := os.ReadFile(path) // #nosec G304 -- a test's temporary file
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(raw, &st)
	return st, err
}
