// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// upstream starts a UDP DNS server on loopback answering every query with
// rcode, and an A record when rcode is success.
func upstream(t *testing.T, rcode int) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		m := new(dns.Msg)
		m.SetRcode(q, rcode)
		if rcode == dns.RcodeSuccess {
			rr, _ := dns.NewRR(q.Question[0].Name + " 60 IN A 192.0.2.7")
			m.Answer = append(m.Answer, rr)
		}
		_ = w.WriteMsg(m)
	})}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String()
}

func forwardA(t *testing.T, servers ...string) *dns.Msg {
	t.Helper()
	f := &UpstreamForwarder{servers: servers, client: &dns.Client{Timeout: 2 * time.Second}}
	q := new(dns.Msg)
	q.SetQuestion("example.com.", dns.TypeA)
	resp, err := f.Forward(context.Background(), q)
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	return resp
}

func TestForwardSkipsAnUpstreamThatRefuses(t *testing.T) {
	t.Parallel()

	resp := forwardA(t, upstream(t, dns.RcodeRefused), upstream(t, dns.RcodeServerFailure), upstream(t, dns.RcodeSuccess))
	if resp.Rcode != dns.RcodeSuccess || len(resp.Answer) != 1 {
		t.Fatalf("got %s with %d answers, want the recursive upstream's", dns.RcodeToString[resp.Rcode], len(resp.Answer))
	}
}

func TestForwardReturnsARefusalWhenNoUpstreamDoesBetter(t *testing.T) {
	t.Parallel()

	if resp := forwardA(t, upstream(t, dns.RcodeRefused)); resp.Rcode != dns.RcodeRefused {
		t.Fatalf("got %s, want REFUSED", dns.RcodeToString[resp.Rcode])
	}
	// NXDOMAIN is an answer, not a failure: the next upstream is not asked.
	if resp := forwardA(t, upstream(t, dns.RcodeNameError), upstream(t, dns.RcodeSuccess)); resp.Rcode != dns.RcodeNameError {
		t.Fatalf("got %s, want NXDOMAIN", dns.RcodeToString[resp.Rcode])
	}
}
