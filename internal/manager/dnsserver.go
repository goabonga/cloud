// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
)

// The agent serves DNS itself rather than running a resolver per VPC. Each
// listener answers from a view - the zones it is authoritative for, rebuilt
// from the store every tick and swapped in atomically - and either forwards
// what falls outside them (a VPC's resolver) or refuses it (the public,
// authoritative-only listener, so it is never an open resolver).

// dnsTTL is the TTL of a record whose spec names none, and of synthesized SOAs.
const dnsTTL = 300

// DNSZone is one zone as a listener serves it: its apex and its records.
type DNSZone struct {
	// Domain is the zone apex, e.g. "internal.example".
	Domain string
	// Records are the zone's resource records, owners fully qualified.
	Records []dns.RR
}

// DNSView is what a listener serves: its zones, and whether names outside
// them are forwarded upstream (a VPC's resolver) or refused (public DNS).
type DNSView struct {
	Zones   []DNSZone
	Forward bool
}

// DNSForwarder resolves a query the listener is not authoritative for.
type DNSForwarder interface {
	Forward(ctx context.Context, q *dns.Msg) (*dns.Msg, error)
}

// Answer builds the response to q from view, forwarding through fwd when q
// falls outside the view's zones and the view forwards.
func Answer(ctx context.Context, view *DNSView, fwd DNSForwarder, q *dns.Msg) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(q)
	if len(q.Question) != 1 {
		m.Rcode = dns.RcodeFormatError
		return m
	}
	question := q.Question[0]
	name := dns.CanonicalName(question.Name)
	zone := view.zoneFor(name)
	if zone == nil {
		if !view.Forward || fwd == nil {
			m.Rcode = dns.RcodeRefused
			return m
		}
		resp, err := fwd.Forward(ctx, q)
		if err != nil {
			m.Rcode = dns.RcodeServerFailure
			return m
		}
		resp.Id = q.Id
		return resp
	}

	m.Authoritative = true
	// Follow CNAMEs within the zone, a few hops at most.
	for hop := 0; hop < 8; hop++ {
		owned := zone.owned(name)
		var cname *dns.CNAME
		matched := false
		for _, rr := range owned {
			if rr.Header().Rrtype == question.Qtype || question.Qtype == dns.TypeANY {
				m.Answer = append(m.Answer, rr)
				matched = true
			} else if c, ok := rr.(*dns.CNAME); ok {
				cname = c
			}
		}
		switch {
		case matched:
			return m
		case name == zone.apex() && question.Qtype == dns.TypeSOA:
			m.Answer = append(m.Answer, zone.soa())
			return m
		case cname != nil:
			m.Answer = append(m.Answer, cname)
			target := dns.CanonicalName(cname.Target)
			if !dns.IsSubDomain(zone.apex(), target) {
				// The target lives elsewhere: the client resolves it itself.
				return m
			}
			name = target
			continue
		}
		// Negative answer: the name does not exist (NXDOMAIN), or exists
		// without that type (NODATA). Either way the SOA goes in authority.
		if len(owned) == 0 && name != zone.apex() {
			m.Rcode = dns.RcodeNameError
		}
		m.Ns = append(m.Ns, zone.soa())
		return m
	}
	return m
}

// zoneFor returns the most specific zone containing name, or nil.
func (v *DNSView) zoneFor(name string) *DNSZone {
	var best *DNSZone
	for i := range v.Zones {
		z := &v.Zones[i]
		if dns.IsSubDomain(z.apex(), name) && (best == nil || dns.CountLabel(z.apex()) > dns.CountLabel(best.apex())) {
			best = z
		}
	}
	return best
}

func (z *DNSZone) apex() string { return dns.CanonicalName(z.Domain) }

// owned returns the zone's records whose owner is name.
func (z *DNSZone) owned(name string) []dns.RR {
	var out []dns.RR
	for _, rr := range z.Records {
		if dns.CanonicalName(rr.Header().Name) == name {
			out = append(out, rr)
		}
	}
	return out
}

// soa is the zone's synthesized SOA, for negative answers and apex queries.
func (z *DNSZone) soa() dns.RR {
	apex := z.apex()
	return &dns.SOA{
		Hdr:     dns.RR_Header{Name: apex, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: dnsTTL},
		Ns:      "ns." + apex,
		Mbox:    "hostmaster." + apex,
		Serial:  1,
		Refresh: 3600,
		Retry:   600,
		Expire:  86400,
		Minttl:  dnsTTL,
	}
}

// ParseRecord builds the resource record for one value of a record spec, e.g.
// ("www", "internal.example", "A", 0, "10.0.1.10").
func ParseRecord(name, domain, rrType string, ttl int, value string) (dns.RR, error) {
	if ttl <= 0 {
		ttl = dnsTTL
	}
	owner := recordFQDN(name, domain)
	rr, err := dns.NewRR(fmt.Sprintf("%s %d IN %s %s", dns.Fqdn(owner), ttl, strings.ToUpper(rrType), value))
	if err != nil {
		return nil, fmt.Errorf("manager: record %s %s %q: %w", owner, rrType, value, err)
	}
	if rr == nil {
		return nil, fmt.Errorf("manager: record %s %s %q is empty", owner, rrType, value)
	}
	return rr, nil
}

// UpstreamForwarder forwards to the host's own resolvers.
type UpstreamForwarder struct {
	servers []string
	client  *dns.Client
}

// NewUpstreamForwarder reads the host's resolvers: systemd-resolved's list of
// real upstreams when present (its stub at 127.0.0.53 would work too, but
// adds a hop), /etc/resolv.conf otherwise.
func NewUpstreamForwarder() *UpstreamForwarder {
	var servers []string
	for _, path := range []string{"/run/systemd/resolve/resolv.conf", "/etc/resolv.conf"} {
		cfg, err := dns.ClientConfigFromFile(path)
		if err != nil || len(cfg.Servers) == 0 {
			continue
		}
		for _, s := range cfg.Servers {
			servers = append(servers, net.JoinHostPort(s, cfg.Port))
		}
		break
	}
	return &UpstreamForwarder{servers: servers, client: &dns.Client{Timeout: 2 * time.Second}}
}

// Forward sends q to each upstream in turn until one answers, retrying over
// TCP when the UDP answer is truncated. An upstream that refuses the query or
// fails it is no answer either: the host's list can hold an authoritative-only
// server, such as the edge's own public DNS, ahead of a recursive one. Its
// answer is only returned when no upstream does better.
func (f *UpstreamForwarder) Forward(ctx context.Context, q *dns.Msg) (*dns.Msg, error) {
	var errs []error
	var fallback *dns.Msg
	for _, s := range f.servers {
		resp, _, err := f.client.ExchangeContext(ctx, q, s)
		if err == nil && resp.Truncated {
			tcp := &dns.Client{Net: "tcp", Timeout: f.client.Timeout}
			resp, _, err = tcp.ExchangeContext(ctx, q, s)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if resp.Rcode == dns.RcodeRefused || resp.Rcode == dns.RcodeServerFailure {
			if fallback == nil {
				fallback = resp
			}
			continue
		}
		return resp, nil
	}
	if fallback != nil {
		return fallback, nil
	}
	if len(errs) == 0 {
		return nil, errors.New("manager: no upstream resolver configured")
	}
	return nil, errors.Join(errs...)
}

// DNSListeners runs the agent's DNS listeners, one UDP and one TCP server per
// address, each serving the view last given for that address.
type DNSListeners struct {
	port string
	fwd  DNSForwarder

	mu      sync.Mutex
	running map[string]*dnsListener
}

type dnsListener struct {
	view atomic.Pointer[DNSView]
	udp  *dns.Server
	tcp  *dns.Server
}

// NewDNSListeners returns listeners on port (53 in production) forwarding
// through fwd.
func NewDNSListeners(port int, fwd DNSForwarder) *DNSListeners {
	return &DNSListeners{port: strconv.Itoa(port), fwd: fwd, running: map[string]*dnsListener{}}
}

// Serve makes addr answer from view, starting its servers on first use and
// swapping the view atomically afterwards. addr must already be assigned.
func (l *DNSListeners) Serve(addr string, view *DNSView) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if cur, ok := l.running[addr]; ok {
		cur.view.Store(view)
		return nil
	}
	ln := &dnsListener{}
	ln.view.Store(view)
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		_ = w.WriteMsg(Answer(ctx, ln.view.Load(), l.fwd, q))
	})
	hostport := net.JoinHostPort(addr, l.port)
	pc, err := net.ListenPacket("udp", hostport)
	if err != nil {
		return fmt.Errorf("manager: dns listen udp %s: %w", hostport, err)
	}
	tl, err := net.Listen("tcp", hostport)
	if err != nil {
		_ = pc.Close()
		return fmt.Errorf("manager: dns listen tcp %s: %w", hostport, err)
	}
	ln.udp = &dns.Server{PacketConn: pc, Handler: handler}
	ln.tcp = &dns.Server{Listener: tl, Handler: handler}
	for _, srv := range []*dns.Server{ln.udp, ln.tcp} {
		go func(s *dns.Server) {
			if err := s.ActivateAndServe(); err != nil {
				fmt.Fprintf(os.Stderr, "manager: dns server %s: %v\n", hostport, err)
			}
		}(srv)
	}
	l.running[addr] = ln
	return nil
}

// Stop shuts addr's servers down. Stopping an address not served is a no-op.
func (l *DNSListeners) Stop(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ln, ok := l.running[addr]; ok {
		_ = ln.udp.Shutdown()
		_ = ln.tcp.Shutdown()
		delete(l.running, addr)
	}
}

// Addresses returns the addresses currently served.
func (l *DNSListeners) Addresses() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.running))
	for a := range l.running {
		out = append(out, a)
	}
	return out
}
