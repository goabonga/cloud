// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package lbproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"time"
)

// Egress listeners are a forward proxy, letting a network's HTTP and HTTPS
// out only to allowed destinations. Three kinds share one policy:
//
//   - egress-http takes plain HTTP redirected to it on its way out and sends
//     each request to the host its Host header names;
//   - egress-tls takes TLS redirected to it and splices the connection to
//     the host its ClientHello's SNI names, without terminating it;
//   - egress-proxy is an explicit proxy: CONNECT tunnels and requests for
//     absolute http:// URLs.
//
// A destination is allowed when its name matches an allowed domain, or when
// it is an address within an allowed CIDR. A name is resolved by the proxy,
// through its own resolver, and only its public addresses are dialed - an
// allowed name pointing inside, at a private, loopback or link-local address,
// is refused unless that address is itself allowed - and the address checked
// is the one dialed, so a name cannot rebind between the two.

// The egress listener protocols.
const (
	ProtocolEgressHTTP  = "egress-http"
	ProtocolEgressTLS   = "egress-tls"
	ProtocolEgressProxy = "egress-proxy"
)

// isEgress reports whether protocol is an egress listener's.
func isEgress(protocol string) bool {
	switch protocol {
	case ProtocolEgressHTTP, ProtocolEgressTLS, ProtocolEgressProxy:
		return true
	}
	return false
}

// EgressPolicy is what an egress listener lets out.
type EgressPolicy struct {
	// AllowedDomains are "example.com", or "*.example.com" for its
	// subdomains.
	AllowedDomains []string `json:"allowedDomains,omitempty"`
	// AllowedCIDRs are addresses reachable by address, and addresses an
	// allowed name may resolve to although they are not public.
	AllowedCIDRs []string `json:"allowedCidrs,omitempty"`
	// Resolver is the DNS server, "addr:port", names are resolved through;
	// empty uses the host's.
	Resolver string `json:"resolver,omitempty"`
}

// egressDialTimeout bounds a connection to a destination.
const egressDialTimeout = 10 * time.Second

// egressTLSPort is the port redirected TLS is sent on: the one it was bound
// for, as only HTTPS is redirected. Tests point it elsewhere.
var egressTLSPort = "443"

// errEgressDenied reports a destination the policy does not allow.
var errEgressDenied = errors.New("egress denied")

// policy is an EgressPolicy ready to use.
type policy struct {
	domains  []string
	nets     []*net.IPNet
	resolver *net.Resolver
	dialer   *net.Dialer
}

func compilePolicy(p *EgressPolicy) (*policy, error) {
	out := &policy{domains: p.AllowedDomains, resolver: net.DefaultResolver, dialer: &net.Dialer{Timeout: egressDialTimeout}}
	for _, c := range p.AllowedCIDRs {
		n, err := parseCIDR(c)
		if err != nil {
			return nil, err
		}
		out.nets = append(out.nets, n)
	}
	if p.Resolver != "" {
		if _, _, err := net.SplitHostPort(p.Resolver); err != nil {
			return nil, fmt.Errorf("resolver %q is not addr:port", p.Resolver)
		}
		d := &net.Dialer{Timeout: 5 * time.Second}
		out.resolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return d.DialContext(ctx, network, p.Resolver)
		}}
	}
	return out, nil
}

func parseCIDR(c string) (*net.IPNet, error) {
	if ip := net.ParseIP(c); ip != nil {
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, nil
	}
	_, n, err := net.ParseCIDR(c)
	if err != nil {
		return nil, fmt.Errorf("allowed cidr %q is neither an address nor a CIDR", c)
	}
	return n, nil
}

func (p *policy) allowsDomain(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, d := range p.domains {
		if matchHost(d, host) {
			return true
		}
	}
	return false
}

func (p *policy) allowsIP(ip net.IP) bool {
	for _, n := range p.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// cgnat is the shared address space, as private as RFC 1918's.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// public reports whether ip is an Internet address: not private, loopback,
// link-local, multicast, unspecified nor shared.
func public(ip net.IP) bool {
	private := ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip)
	return !private
}

// dial connects to host:port when the policy allows it.
func (p *policy) dial(ctx context.Context, host, port string) (net.Conn, error) {
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		if !p.allowsIP(ip) {
			return nil, fmt.Errorf("%w: address %s", errEgressDenied, ip)
		}
		return p.dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
	}
	if !p.allowsDomain(host) {
		return nil, fmt.Errorf("%w: %s", errEgressDenied, host)
	}
	addrs, err := p.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	var errs []error
	for _, a := range addrs {
		if !public(a.IP) && !p.allowsIP(a.IP) {
			errs = append(errs, fmt.Errorf("%w: %s resolves to %s, not public", errEgressDenied, host, a.IP))
			continue
		}
		c, err := p.dialer.DialContext(ctx, "tcp", net.JoinHostPort(a.IP.String(), port))
		if err == nil {
			return c, nil
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("resolve %s: no address", host)
	}
	return nil, errors.Join(errs...)
}

// egressHandler serves an egress-http or egress-proxy listener.
type egressHandler struct {
	l *listener
}

func (h *egressHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cfg := h.l.cfg.Load()
	pol := h.l.policy.Load()
	if r.Method == http.MethodConnect {
		if cfg.Protocol != ProtocolEgressProxy {
			http.Error(w, "CONNECT is only for the explicit proxy", http.StatusMethodNotAllowed)
			return
		}
		h.tunnel(w, r, pol)
		return
	}
	host, port := hostOnly(r.Host), "80"
	if _, p, err := net.SplitHostPort(r.Host); err == nil {
		port = p
	}
	if cfg.Protocol == ProtocolEgressProxy {
		// An explicit proxy takes absolute URLs: GET http://example.com/.
		if r.URL.Scheme != "http" || r.URL.Host == "" {
			http.Error(w, "the egress proxy takes CONNECT or absolute http:// URLs", http.StatusBadRequest)
			return
		}
		host = r.URL.Hostname()
		if p := r.URL.Port(); p != "" {
			port = p
		}
	}
	if host == "" {
		http.Error(w, "no host to send the request to", http.StatusBadRequest)
		return
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = net.JoinHostPort(host, port)
			pr.Out.Host = pr.In.Host
			pr.Out.Header.Del("Proxy-Connection")
			pr.Out.Header.Del("Proxy-Authorization")
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return pol.dial(ctx, host, port)
			},
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     30 * time.Second,
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			h.denyOrFail(w, cfg.Name, host, err)
		},
	}
	rp.ServeHTTP(w, r)
}

// tunnel serves a CONNECT: it dials the destination, says so, and splices.
func (h *egressHandler) tunnel(w http.ResponseWriter, r *http.Request, pol *policy) {
	cfg := h.l.cfg.Load()
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		http.Error(w, "CONNECT needs host:port", http.StatusBadRequest)
		return
	}
	if _, err := strconv.Atoi(port); err != nil {
		http.Error(w, "CONNECT needs a numeric port", http.StatusBadRequest)
		return
	}
	upstream, err := pol.dial(r.Context(), host, port)
	if err != nil {
		h.denyOrFail(w, cfg.Name, host, err)
		return
	}
	defer func() { _ = upstream.Close() }()
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "cannot tunnel", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	splice(bufferedConn{Conn: client, r: buf.Reader}, upstream)
}

func (h *egressHandler) denyOrFail(w http.ResponseWriter, listener, host string, err error) {
	if errors.Is(err, errEgressDenied) {
		h.l.p.log.Info("egress denied", "listener", listener, "host", host, "reason", err)
		http.Error(w, "egress to "+host+" is not allowed", http.StatusForbidden)
		return
	}
	h.l.p.log.Warn("egress failed", "listener", listener, "host", host, "err", err)
	http.Error(w, "cannot reach "+host, http.StatusBadGateway)
}

// bufferedConn reads what the HTTP server buffered before the hijack first.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func (c bufferedConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return c.Close()
}

// serveEgressTLS splices a redirected TLS connection to the host its SNI
// names, replaying the ClientHello read to learn it.
func (l *listener) serveEgressTLS(c net.Conn) {
	cfg := l.cfg.Load()
	_ = c.SetReadDeadline(time.Now().Add(helloTimeout))
	sni, hello, err := peekSNI(c)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil || sni == "" {
		l.p.log.Info("egress denied", "listener", cfg.Name, "reason", "no SNI to filter on")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), egressDialTimeout)
	upstream, err := l.policy.Load().dial(ctx, sni, egressTLSPort)
	cancel()
	if err != nil {
		if errors.Is(err, errEgressDenied) {
			l.p.log.Info("egress denied", "listener", cfg.Name, "host", sni, "reason", err)
		} else {
			l.p.log.Warn("egress failed", "listener", cfg.Name, "host", sni, "err", err)
		}
		return
	}
	defer func() { _ = upstream.Close() }()
	if _, err := upstream.Write(hello); err != nil {
		return
	}
	splice(c, upstream)
}
