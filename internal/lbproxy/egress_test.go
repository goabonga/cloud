// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package lbproxy

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// egressListener returns an egress listener of protocol on a free port.
func egressListener(t *testing.T, protocol string, pol EgressPolicy) Listener {
	t.Helper()
	return Listener{Name: protocol, Address: "127.0.0.1", Port: freePort(t), Protocol: protocol, Egress: &pol}
}

// loopback lets "localhost" through to loopback servers: the policy refuses
// a name resolving to a private address unless that address is allowed.
var loopback = EgressPolicy{AllowedDomains: []string{"localhost"}, AllowedCIDRs: []string{"127.0.0.0/8"}}

func portOf(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Port()
}

func TestEgressHTTPSendsAllowedHostsOnly(t *testing.T) {
	t.Parallel()

	backend := named(t, "outside")
	l := egressListener(t, ProtocolEgressHTTP, loopback)
	startProxy(t, &Config{Listeners: []Listener{l}})

	// A request redirected to the proxy: sent to the proxy, naming its host.
	send := func(host string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr(l.Port)+"/", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	if code, body := send("localhost:" + portOf(t, backend.URL)); code != http.StatusOK || body != "outside" {
		t.Fatalf("allowed host: %d %q", code, body)
	}
	if code, _ := send("example.org:" + portOf(t, backend.URL)); code != http.StatusForbidden {
		t.Fatalf("a host outside the allowed domains: %d, want 403", code)
	}
}

func TestEgressRefusesAnAllowedNamePointingInside(t *testing.T) {
	t.Parallel()

	backend := named(t, "inside")
	// localhost is allowed by name, but not its loopback address.
	l := egressListener(t, ProtocolEgressHTTP, EgressPolicy{AllowedDomains: []string{"localhost"}})
	startProxy(t, &Config{Listeners: []Listener{l}})
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr(l.Port)+"/", nil)
	req.Host = "localhost:" + portOf(t, backend.URL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d, want 403: a name resolving to a private address", resp.StatusCode)
	}
}

func TestEgressProxyTunnelsAndForwardsAbsoluteURLs(t *testing.T) {
	t.Parallel()

	ca := newCA(t, "outside ca")
	tlsBackend := tlsServer(t, "tls outside", issue(t, ca, "localhost"))
	plain := named(t, "plain outside")
	l := egressListener(t, ProtocolEgressProxy, loopback)
	startProxy(t, &Config{Listeners: []Listener{l}})

	proxyURL, _ := url.Parse("http://" + addr(l.Port))
	roots := clientTrusting(ca, "").Transport.(*http.Transport).TLSClientConfig.RootCAs
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}

	// HTTPS through a CONNECT tunnel, end to end: the proxy sees no plaintext.
	if code, body := get(t, client, "https://localhost:"+portOf(t, tlsBackend.URL)+"/"); code != http.StatusOK || body != "tls outside" {
		t.Fatalf("CONNECT: %d %q", code, body)
	}
	// Plain HTTP as an absolute URL.
	if code, body := get(t, client, "http://localhost:"+portOf(t, plain.URL)+"/"); code != http.StatusOK || body != "plain outside" {
		t.Fatalf("absolute URL: %d %q", code, body)
	}
	// A CONNECT to a host the policy refuses is answered 403.
	conn, err := net.Dial("tcp", addr(l.Port))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, _ = conn.Write([]byte("CONNECT example.org:443 HTTP/1.1\r\nHost: example.org:443\r\n\r\n"))
	line := make([]byte, 12)
	_, _ = io.ReadFull(conn, line)
	if !strings.Contains(string(line), "403") {
		t.Fatalf("CONNECT to a refused host: %q", line)
	}
}

func TestEgressTLSSplicesBySNI(t *testing.T) { //nolint:paralleltest // it points egressTLSPort at its backend
	ca := newCA(t, "outside ca")
	backend := tlsServer(t, "tls outside", issue(t, ca, "localhost"))
	old := egressTLSPort
	egressTLSPort = portOf(t, backend.URL)
	t.Cleanup(func() { egressTLSPort = old })

	l := egressListener(t, ProtocolEgressTLS, loopback)
	startProxy(t, &Config{Listeners: []Listener{l}})

	// TLS redirected to the proxy, for localhost: spliced to it untouched.
	c := clientTrusting(ca, addr(l.Port))
	if code, body := get(t, c, "https://localhost/"); code != http.StatusOK || body != "tls outside" {
		t.Fatalf("allowed SNI: %d %q", code, body)
	}
	// For a name outside the allowed domains, the connection is dropped.
	raw, err := tls.Dial("tcp", addr(l.Port), &tls.Config{ServerName: "example.org", InsecureSkipVerify: true}) // #nosec G402 -- the handshake must fail before any verification
	if err == nil {
		_ = raw.Close()
		t.Fatal("a refused SNI must not get a handshake")
	}
}

func TestEgressListenerValidation(t *testing.T) {
	t.Parallel()

	for name, l := range map[string]Listener{
		"no policy":        {Name: "a", Port: 3128, Protocol: ProtocolEgressProxy},
		"with a group":     {Name: "a", Port: 3128, Protocol: ProtocolEgressProxy, Egress: &EgressPolicy{}, DefaultTargetGroup: "g"},
		"bad cidr":         {Name: "a", Port: 3128, Protocol: ProtocolEgressProxy, Egress: &EgressPolicy{AllowedCIDRs: []string{"nope"}}},
		"bad resolver":     {Name: "a", Port: 3128, Protocol: ProtocolEgressProxy, Egress: &EgressPolicy{Resolver: "10.0.0.1"}},
		"policy on a http": {Name: "a", Port: 80, Protocol: ProtocolHTTP, DefaultTargetGroup: "g", Egress: &EgressPolicy{}},
	} {
		cfg := &Config{Listeners: []Listener{l}, TargetGroups: []TargetGroup{{Name: "g", Protocol: ProtocolHTTP}}}
		cfg.setDefaults()
		if err := cfg.Validate(); err == nil {
			t.Fatalf("%s: want an error", name)
		}
	}
}
