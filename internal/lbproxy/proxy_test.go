// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package lbproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var errTest = errors.New("test failure")

func TestHTTPListenerRoutesByHostAndPath(t *testing.T) {
	t.Parallel()

	api, static, web := named(t, "api"), named(t, "static"), named(t, "web")
	port := freePort(t)
	startProxy(t, &Config{
		Listeners: []Listener{{Name: "http", Address: "127.0.0.1", Port: port, Protocol: "http", DefaultTargetGroup: "web",
			Rules: []Rule{{Host: "api.demo.test", TargetGroup: "api"}, {PathPrefix: "/static", TargetGroup: "static"}}}},
		TargetGroups: []TargetGroup{
			{Name: "api", Protocol: "http", Targets: []Target{targetOf(t, api.URL)}},
			{Name: "static", Protocol: "http", Targets: []Target{targetOf(t, static.URL)}},
			{Name: "web", Protocol: "http", Targets: []Target{targetOf(t, web.URL)}},
		},
	})
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, n, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, n, addr(port))
	}}}
	for url, want := range map[string]string{
		"http://api.demo.test/v1":          "api",
		"http://www.demo.test/static/a.js": "static",
		"http://www.demo.test/":            "web",
	} {
		if code, body := get(t, c, url); code != 200 || body != want {
			t.Errorf("%s: %d %q, want %q", url, code, body, want)
		}
	}
}

func TestHTTPSTerminatesWithTheCertificateOfTheSNI(t *testing.T) {
	t.Parallel()

	ca := newCA(t, "public")
	var proto, forwardedFor, host atomic.Value
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.UserAgent() == "infra-lb-health-check" {
			w.WriteHeader(http.StatusOK)
			return
		}
		proto.Store(r.Header.Get("X-Forwarded-Proto"))
		forwardedFor.Store(r.Header.Get("X-Forwarded-For"))
		host.Store(r.Host)
		_, _ = fmt.Fprint(w, "web")
	}))
	t.Cleanup(backend.Close)
	port := freePort(t)
	startProxy(t, &Config{
		Listeners: []Listener{{Name: "https", Address: "127.0.0.1", Port: port, Protocol: "https", DefaultTargetGroup: "web",
			Certificates: []Certificate{issue(t, ca, "demo.test", "www.demo.test"), issue(t, ca, "*.apps.test")}}},
		TargetGroups: []TargetGroup{{Name: "web", Protocol: "http", Targets: []Target{targetOf(t, backend.URL)}}},
	})
	c := clientTrusting(ca, addr(port))
	for _, name := range []string{"www.demo.test", "x.apps.test"} {
		if code, body := get(t, c, "https://"+name+"/"); code != 200 || body != "web" {
			t.Fatalf("%s: %d %q", name, code, body)
		}
	}
	if proto.Load() != "https" || forwardedFor.Load() != "127.0.0.1" || host.Load() != "x.apps.test" {
		t.Fatalf("forwarded headers: proto=%v for=%v host=%v", proto.Load(), forwardedFor.Load(), host.Load())
	}
	// A name no certificate covers gets the first one, which fails to verify.
	if resp, err := c.Get("https://other.test/"); err == nil {
		_ = resp.Body.Close()
		t.Fatal("other.test must get the default certificate, not one naming it")
	}
}

func TestReencryptVerifiesTheBackendsAgainstTheirCA(t *testing.T) {
	t.Parallel()

	public, internal, rogue := newCA(t, "public"), newCA(t, "internal"), newCA(t, "rogue")
	backend := tlsServer(t, "web", issue(t, internal, "www.demo.test"))
	port := freePort(t)
	cfg := func(ca string) *Config {
		return &Config{
			Listeners: []Listener{{Name: "https", Address: "127.0.0.1", Port: port, Protocol: "https", TLSMode: "reencrypt",
				Certificates: []Certificate{issue(t, public, "www.demo.test")}, DefaultTargetGroup: "web"}},
			TargetGroups: []TargetGroup{{Name: "web", Protocol: "https", BackendCAPEM: ca, Targets: []Target{targetOf(t, backend.URL)},
				HealthCheck: HealthCheck{IntervalSeconds: 100, TimeoutSeconds: 50}}},
		}
	}
	p := startProxy(t, cfg(string(internal.CertPEM)))
	c := clientTrusting(public, addr(port))
	if code, body := get(t, c, "https://www.demo.test/"); code != 200 || body != "web" {
		t.Fatalf("trusted backend: %d %q", code, body)
	}

	bad := cfg(string(rogue.CertPEM))
	bad.setDefaults()
	p.Apply(bad)
	if code, _ := get(t, c, "https://www.demo.test/"); code != http.StatusBadGateway {
		t.Fatalf("a backend the CA does not sign must give 502, got %d", code)
	}
}

func TestPassthroughRoutesBySNIWithoutTerminating(t *testing.T) {
	t.Parallel()

	ca := newCA(t, "internal")
	a := tlsServer(t, "a", issue(t, ca, "a.test"))
	b := tlsServer(t, "b", issue(t, ca, "x.b.test"))
	port := freePort(t)
	startProxy(t, &Config{
		Listeners: []Listener{{Name: "sni", Address: "127.0.0.1", Port: port, Protocol: "tls", DefaultTargetGroup: "a",
			Rules: []Rule{{Host: "*.b.test", TargetGroup: "b"}}}},
		TargetGroups: []TargetGroup{
			{Name: "a", Protocol: "tcp", Targets: []Target{targetOf(t, a.URL)}},
			{Name: "b", Protocol: "https", Targets: []Target{targetOf(t, b.URL)}, HealthCheck: HealthCheck{IntervalSeconds: 100, TimeoutSeconds: 50}},
		},
	})
	// The client verifies the backends' own certificates: the proxy only
	// routes the bytes.
	c := clientTrusting(ca, addr(port))
	for name, want := range map[string]string{"a.test": "a", "x.b.test": "b"} {
		if code, body := get(t, c, "https://"+name+"/"); code != 200 || body != want {
			t.Errorf("%s: %d %q, want %q", name, code, body, want)
		}
	}
}

func TestTCPListenerSplices(t *testing.T) {
	t.Parallel()

	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = echo.Close() })
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	port := freePort(t)
	startProxy(t, &Config{
		Listeners:    []Listener{{Name: "tcp", Address: "127.0.0.1", Port: port, Protocol: "tcp", DefaultTargetGroup: "echo"}},
		TargetGroups: []TargetGroup{{Name: "echo", Protocol: "tcp", Targets: []Target{targetOf(t, "http://"+echo.Addr().String())}}},
	})
	c, err := net.Dial("tcp4", addr(port))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_, _ = fmt.Fprintln(c, "ping")
	_ = c.(*net.TCPConn).CloseWrite()
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil || line != "ping\n" {
		t.Fatalf("echo: %q %v", line, err)
	}
}

// flaky answers its name, or 500 on /healthz while sick is set.
func flaky(t *testing.T, name string, sick *atomic.Bool) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" && sick.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, name)
	}))
	t.Cleanup(s.Close)
	return s
}

func TestHealthChecksEjectTargetsAndFailOpen(t *testing.T) {
	t.Parallel()

	var sickA, sickB atomic.Bool
	a, b := flaky(t, "a", &sickA), flaky(t, "b", &sickB)
	ta, tb := targetOf(t, a.URL), targetOf(t, b.URL)
	port := freePort(t)
	p := startProxy(t, &Config{
		Listeners: []Listener{{Name: "http", Address: "127.0.0.1", Port: port, Protocol: "http", DefaultTargetGroup: "web"}},
		TargetGroups: []TargetGroup{{Name: "web", Protocol: "http", Targets: []Target{ta, tb},
			// 200ms checks: tight timeouts would fail the healthy target too
			// on a loaded machine, and fail open.
			HealthCheck: HealthCheck{Path: "/healthz", IntervalSeconds: 20, TimeoutSeconds: 20, HealthyThreshold: 2, UnhealthyThreshold: 2}}},
	})
	if h := health(p, "web", ta); h != HealthUnknown && h != HealthHealthy {
		t.Fatalf("a target starts unknown: %s", h)
	}
	eventually(t, "both healthy", func() bool { return health(p, "web", ta) == HealthHealthy && health(p, "web", tb) == HealthHealthy })

	sickA.Store(true)
	eventually(t, "a unhealthy", func() bool { return health(p, "web", ta) == HealthUnhealthy })
	c := &http.Client{Timeout: 5 * time.Second}
	for range 6 {
		if _, body := get(t, c, "http://"+addr(port)+"/"); body != "b" {
			t.Fatalf("an unhealthy target got traffic: %q", body)
		}
	}

	sickB.Store(true)
	eventually(t, "b unhealthy", func() bool { return health(p, "web", tb) == HealthUnhealthy })
	seen := map[string]bool{}
	for range 6 {
		_, body := get(t, c, "http://"+addr(port)+"/")
		seen[body] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("with every target unhealthy, traffic must fail open over all: %v", seen)
	}

	sickA.Store(false)
	eventually(t, "a healthy again", func() bool { return health(p, "web", ta) == HealthHealthy })
}

func TestFailingAnswersEjectATargetPassively(t *testing.T) {
	t.Parallel()

	var badHits atomic.Int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			return
		}
		badHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(bad.Close)
	good := named(t, "good")
	port := freePort(t)
	p := startProxy(t, &Config{
		Listeners: []Listener{{Name: "http", Address: "127.0.0.1", Port: port, Protocol: "http", DefaultTargetGroup: "web"}},
		TargetGroups: []TargetGroup{{Name: "web", Protocol: "http", Targets: []Target{targetOf(t, bad.URL), targetOf(t, good.URL)},
			// Active checks pass but are rare: only traffic can eject it.
			HealthCheck: HealthCheck{Path: "/healthz", IntervalSeconds: 1000, TimeoutSeconds: 100, UnhealthyThreshold: 3}}},
	})
	c := &http.Client{Timeout: 5 * time.Second}
	for range 12 {
		get(t, c, "http://"+addr(port)+"/")
	}
	if n := badHits.Load(); n != 3 {
		t.Fatalf("the failing target got %d requests, want 3 before its ejection", n)
	}
	if h := health(p, "web", targetOf(t, bad.URL)); h != HealthUnhealthy {
		t.Fatalf("health %s, want unhealthy", h)
	}
}

func TestUnreachableTargetIsRetriedOnAnother(t *testing.T) {
	t.Parallel()

	var bodies atomic.Value
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The health checks GET this backend too.
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			bodies.Store(string(b))
		}
		_, _ = fmt.Fprint(w, "live")
	}))
	t.Cleanup(live.Close)
	dead := Target{Address: "127.0.0.1", Port: freePort(t)}
	port := freePort(t)
	startProxy(t, &Config{
		Listeners: []Listener{{Name: "http", Address: "127.0.0.1", Port: port, Protocol: "http", DefaultTargetGroup: "web"}},
		TargetGroups: []TargetGroup{{Name: "web", Protocol: "http", Targets: []Target{dead, targetOf(t, live.URL)},
			HealthCheck: HealthCheck{IntervalSeconds: 1000, TimeoutSeconds: 100, UnhealthyThreshold: 100}}},
	})
	c := &http.Client{Timeout: 5 * time.Second}
	for i := range 4 {
		resp, err := c.Post("http://"+addr(port)+"/", "text/plain", strings.NewReader(fmt.Sprint("order-", i)))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != "live" || bodies.Load() != fmt.Sprint("order-", i) {
			t.Fatalf("POST %d: %d %q, backend got %v", i, resp.StatusCode, body, bodies.Load())
		}
	}
}

func TestReloadKeepsTheSocketSwapsCertificatesAndKeepsHealth(t *testing.T) {
	t.Parallel()

	ca := newCA(t, "public")
	web := named(t, "web")
	tw := targetOf(t, web.URL)
	port := freePort(t)
	cfg := func(names ...string) *Config {
		c := &Config{
			Listeners:    []Listener{{Name: "https", Address: "127.0.0.1", Port: port, Protocol: "https", DefaultTargetGroup: "web", Certificates: []Certificate{issue(t, ca, names...)}}},
			TargetGroups: []TargetGroup{{Name: "web", Protocol: "http", Targets: []Target{tw}, HealthCheck: HealthCheck{IntervalSeconds: 20, TimeoutSeconds: 20}}},
		}
		c.setDefaults()
		return c
	}
	p := startProxy(t, cfg("one.test"))
	eventually(t, "healthy", func() bool { return health(p, "web", tw) == HealthHealthy })
	p.mu.Lock()
	before := p.listeners["https"]
	p.mu.Unlock()

	p.Apply(cfg("two.test"))
	p.mu.Lock()
	after := p.listeners["https"]
	p.mu.Unlock()
	if after != before {
		t.Fatal("a listener whose socket is unchanged must be kept")
	}
	if h := health(p, "web", tw); h != HealthHealthy {
		t.Fatalf("a kept target must keep its health, got %s", h)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CertPEM)
	conn, err := tls.Dial("tcp4", addr(port), &tls.Config{RootCAs: pool, ServerName: "two.test", MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatalf("the new certificate must be served: %v", err)
	}
	_ = conn.Close()
}

func TestRunKeepsTheRunningConfigurationAndReportsStatus(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath, statusPath := dir+"/lb.json", dir+"/status.json"
	web := named(t, "web")
	port, port2 := freePort(t), freePort(t)
	writeCfg := func(s string) {
		if err := writeFile(cfgPath, s); err != nil {
			t.Fatal(err)
		}
	}
	tw := targetOf(t, web.URL)
	listener := func(name string, port int) string {
		return fmt.Sprintf(`{"name":%q,"address":"127.0.0.1","port":%d,"protocol":"http","defaultTargetGroup":"web"}`, name, port)
	}
	group := fmt.Sprintf(`{"name":"web","protocol":"http","targets":[{"id":"web-1","address":%q,"port":%d}]}`, tw.Address, tw.Port)
	first := `{"listeners":[` + listener("http", port) + `],"targetGroups":[` + group + `]}`
	writeCfg(first)

	p := New(testLogger())
	p.unit, p.drain = 10*time.Millisecond, time.Second
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	reload := make(chan struct{}, 1)
	go func() {
		done <- Run(ctx, p, RunOptions{ConfigPath: cfgPath, StatusPath: statusPath, Poll: 20 * time.Millisecond, Reload: reload})
	}()
	t.Cleanup(func() { cancel(); <-done })

	waitStatus(t, statusPath, "first status", func(st Status) bool {
		return st.ConfigGeneration == 1 && len(st.Listeners) == 1 && st.Listeners[0].Bound
	})
	c := &http.Client{Timeout: 5 * time.Second}
	if _, body := get(t, c, "http://"+addr(port)+"/"); body != "web" {
		t.Fatalf("served %q", body)
	}

	writeCfg(`{"listeners":[{"name":"http","port":0}]}`)
	reload <- struct{}{}
	waitStatus(t, statusPath, "rejection reported", func(st Status) bool {
		return st.LastError != "" && st.ConfigGeneration == 1
	})
	if _, body := get(t, c, "http://"+addr(port)+"/"); body != "web" {
		t.Fatalf("a rejected configuration must leave the running one: %q", body)
	}

	// The running configuration put back: nothing to apply, nothing wrong.
	writeCfg(first)
	reload <- struct{}{}
	waitStatus(t, statusPath, "rejection cleared", func(st Status) bool {
		return st.LastError == "" && st.ConfigGeneration == 1
	})

	writeCfg(`{"listeners":[` + listener("http", port) + `,` + listener("http2", port2) + `],"targetGroups":[` + group + `]}`)
	reload <- struct{}{}
	waitStatus(t, statusPath, "second generation", func(st Status) bool {
		if st.ConfigGeneration != 2 || st.LastError != "" || len(st.Listeners) != 2 {
			return false
		}
		tg := st.TargetGroups[0].Targets[0]
		return st.Listeners[1].Bound && tg.ID == "web-1" && tg.Health != ""
	})
	if _, body := get(t, c, "http://"+addr(port2)+"/"); body != "web" {
		t.Fatalf("new listener served %q", body)
	}
}

func TestHTTPSHealthCheckVerifiesTheChainWithoutAName(t *testing.T) {
	t.Parallel()

	internal, rogue := newCA(t, "internal"), newCA(t, "rogue")
	backend := tlsServer(t, "web", issue(t, internal, "web.internal.demo"))
	probe := func(ca []byte) error {
		c := &checker{
			hc:     HealthCheck{Protocol: ProtocolHTTPS, Path: "/"},
			target: targetOf(t, backend.URL),
			roots:  backendRoots(TargetGroup{BackendCAPEM: string(ca)}),
		}
		return c.probe(context.Background())
	}
	if err := probe(internal.CertPEM); err != nil {
		t.Fatalf("a backend its CA signs must pass, whatever its name: %v", err)
	}
	if err := probe(rogue.CertPEM); err == nil {
		t.Fatal("a backend another CA signs must fail")
	}
}
