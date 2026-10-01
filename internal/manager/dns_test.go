// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"net"
	"slices"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// fakeDNSBackend records the views the reconciler hands out.
type fakeDNSBackend struct {
	vpc     map[string]*manager.DNSView
	addrs   map[string]string
	public  map[string]*manager.DNSView
	stopped []string
}

func newFakeDNSBackend() *fakeDNSBackend {
	return &fakeDNSBackend{vpc: map[string]*manager.DNSView{}, addrs: map[string]string{}, public: map[string]*manager.DNSView{}}
}

func (f *fakeDNSBackend) ServeVPC(_ context.Context, vpcID, _, addr string, view *manager.DNSView) error {
	f.vpc[vpcID], f.addrs[vpcID] = view, addr
	return nil
}

func (f *fakeDNSBackend) StopVPC(_ context.Context, vpcID string) error {
	delete(f.vpc, vpcID)
	f.stopped = append(f.stopped, vpcID)
	return nil
}

func (f *fakeDNSBackend) ServedVPCs() []string {
	var ids []string
	for id := range f.vpc {
		ids = append(ids, id)
	}
	return ids
}

func (f *fakeDNSBackend) ServePublic(_ context.Context, addr string, view *manager.DNSView) error {
	f.public[addr] = view
	return nil
}

func domains(v *manager.DNSView) []string {
	var out []string
	for _, z := range v.Zones {
		out = append(out, z.Domain)
	}
	sort.Strings(out)
	return out
}

func TestDNSGivesEachVPCItsPrivateZonesPlusThePublicOnes(t *testing.T) {
	t.Parallel()

	env := newDNSEnv(t)
	env.putVPC(t, "vpc-1", "10.20.0.0/16", "br-1")
	env.putVPC(t, "vpc-2", "10.30.0.0/16", "br-2")
	env.putZone(t, "z-priv", "internal.example", "vpc-1")
	env.putPublicZone(t, "z-pub", "example.test")
	env.putRecord(t, "r-web", "z-priv", "web", "A", "10.20.1.10")
	env.putRecord(t, "r-www", "z-pub", "www", "A", "203.0.113.10")

	be := newFakeDNSBackend()
	if err := manager.NewDNSReconciler(env.zones, env.records, env.vpcs, be).WithPublicAddress("203.0.113.53").ReconcileAll(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := domains(be.vpc["vpc-1"]); !slices.Equal(got, []string{"example.test", "internal.example"}) || !be.vpc["vpc-1"].Forward {
		t.Fatalf("vpc-1 view: %v forward=%v", got, be.vpc["vpc-1"].Forward)
	}
	if got := domains(be.vpc["vpc-2"]); !slices.Equal(got, []string{"example.test"}) {
		t.Fatalf("vpc-2 must not see vpc-1's private zone: %v", got)
	}
	if be.addrs["vpc-1"] != "10.20.0.1" {
		t.Fatalf("vpc-1 resolver on %q, want its first address 10.20.0.1", be.addrs["vpc-1"])
	}
	pub := be.public["203.0.113.53"]
	if pub == nil || pub.Forward || !slices.Equal(domains(pub), []string{"example.test"}) {
		t.Fatalf("public view: %+v", pub)
	}
	for _, uid := range []string{"z-priv", "z-pub"} {
		if z, _ := env.zones.Get(uid); z.Status.Phase != resource.PhaseReady {
			t.Fatalf("zone %s phase %q", uid, z.Status.Phase)
		}
	}
}

func TestDNSStopsTheResolverOfAGoneVPC(t *testing.T) {
	t.Parallel()

	env := newDNSEnv(t)
	be := newFakeDNSBackend()
	be.vpc["vpc-gone"] = &manager.DNSView{}
	if err := manager.NewDNSReconciler(env.zones, env.records, env.vpcs, be).ReconcileAll(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !slices.Equal(be.stopped, []string{"vpc-gone"}) {
		t.Fatalf("stopped %v", be.stopped)
	}
}

func TestDNSMarksAnUnparsableRecord(t *testing.T) {
	t.Parallel()

	env := newDNSEnv(t)
	env.putVPC(t, "vpc-1", "10.20.0.0/16", "br-1")
	env.putZone(t, "z", "internal.example", "vpc-1")
	env.putRecord(t, "bad", "z", "web", "A", "not-an-address")
	be := newFakeDNSBackend()
	_ = manager.NewDNSReconciler(env.zones, env.records, env.vpcs, be).ReconcileAll(context.Background())
	if r, _ := env.records.Get("bad"); r.Status.Phase != resource.PhaseError {
		t.Fatalf("phase %q, want Error", r.Status.Phase)
	}
	if n := len(be.vpc["vpc-1"].Zones[0].Records); n != 0 {
		t.Fatalf("an unparsable record must not be served: %d records", n)
	}
}

// staticForwarder answers every forwarded query with one A record.
type staticForwarder struct{ called int }

func (f *staticForwarder) Forward(_ context.Context, q *dns.Msg) (*dns.Msg, error) {
	f.called++
	m := new(dns.Msg)
	m.SetReply(q)
	rr, _ := dns.NewRR(q.Question[0].Name + " 60 IN A 93.184.215.14")
	m.Answer = []dns.RR{rr}
	return m, nil
}

func testView(t *testing.T, forward bool) *manager.DNSView {
	t.Helper()
	var rrs []dns.RR
	for _, r := range [][3]string{
		{"web", "A", "10.20.1.10"},
		{"web", "A", "10.20.1.11"},
		{"www", "CNAME", "web.internal.example."},
		{"mail", "TXT", `"v=spf1 -all"`},
	} {
		rr, err := manager.ParseRecord(r[0], "internal.example", r[1], 0, r[2])
		if err != nil {
			t.Fatal(err)
		}
		rrs = append(rrs, rr)
	}
	sub, _ := manager.ParseRecord("api", "eu.internal.example", "A", 0, "10.20.2.10")
	return &manager.DNSView{Forward: forward, Zones: []manager.DNSZone{
		{Domain: "internal.example", Records: rrs},
		{Domain: "eu.internal.example", Records: []dns.RR{sub}},
	}}
}

func ask(t *testing.T, view *manager.DNSView, fwd manager.DNSForwarder, name string, qtype uint16) *dns.Msg {
	t.Helper()
	q := new(dns.Msg)
	q.SetQuestion(dns.Fqdn(name), qtype)
	return manager.Answer(context.Background(), view, fwd, q)
}

func TestAnswerServesTheZoneAuthoritatively(t *testing.T) {
	t.Parallel()

	view := testView(t, true)
	fwd := &staticForwarder{}
	for _, tc := range []struct {
		name   string
		qtype  uint16
		rcode  int
		answer int
		soa    bool
	}{
		{"web.internal.example", dns.TypeA, dns.RcodeSuccess, 2, false},
		{"WEB.Internal.Example", dns.TypeA, dns.RcodeSuccess, 2, false},     // case-insensitive
		{"www.internal.example", dns.TypeA, dns.RcodeSuccess, 3, false},     // CNAME + the two A
		{"web.internal.example", dns.TypeAAAA, dns.RcodeSuccess, 0, true},   // NODATA
		{"nope.internal.example", dns.TypeA, dns.RcodeNameError, 0, true},   // NXDOMAIN
		{"internal.example", dns.TypeSOA, dns.RcodeSuccess, 1, false},       // apex SOA
		{"api.eu.internal.example", dns.TypeA, dns.RcodeSuccess, 1, false},  // most specific zone
		{"web.eu.internal.example", dns.TypeA, dns.RcodeNameError, 0, true}, // not the parent zone's web
		{"mail.internal.example", dns.TypeTXT, dns.RcodeSuccess, 1, false},
	} {
		m := ask(t, view, fwd, tc.name, tc.qtype)
		if m.Rcode != tc.rcode || len(m.Answer) != tc.answer || !m.Authoritative || (len(m.Ns) == 1) != tc.soa {
			t.Fatalf("%s %s: rcode=%d answers=%d aa=%v ns=%d", tc.name, dns.TypeToString[tc.qtype], m.Rcode, len(m.Answer), m.Authoritative, len(m.Ns))
		}
	}
	if fwd.called != 0 {
		t.Fatalf("names inside the zones must not be forwarded (%d)", fwd.called)
	}
}

func TestAnswerForwardsOrRefusesNamesOutsideTheZones(t *testing.T) {
	t.Parallel()

	fwd := &staticForwarder{}
	if m := ask(t, testView(t, true), fwd, "example.org", dns.TypeA); m.Rcode != dns.RcodeSuccess || len(m.Answer) != 1 || fwd.called != 1 {
		t.Fatalf("resolver: rcode=%d answers=%d forwarded=%d", m.Rcode, len(m.Answer), fwd.called)
	}
	if m := ask(t, testView(t, false), fwd, "example.org", dns.TypeA); m.Rcode != dns.RcodeRefused || fwd.called != 1 {
		t.Fatalf("public DNS must refuse recursion: rcode=%d forwarded=%d", m.Rcode, fwd.called)
	}
}

func TestDNSListenersAnswerOverUDPAndTCP(t *testing.T) {
	t.Parallel()

	// A free port on 127.0.0.1; no root needed off port 53.
	probe, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.LocalAddr().(*net.UDPAddr).Port
	_ = probe.Close()

	l := manager.NewDNSListeners(port, &staticForwarder{})
	if err := l.Serve("", "127.0.0.1", testView(t, true)); err != nil {
		t.Fatalf("serve: %v", err)
	}
	t.Cleanup(func() { l.Stop("", "127.0.0.1") })
	server := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	q := new(dns.Msg)
	q.SetQuestion("web.internal.example.", dns.TypeA)
	for _, proto := range []string{"udp", "tcp"} {
		var resp *dns.Msg
		for try := 0; try < 20; try++ { // the servers start asynchronously
			c := &dns.Client{Net: proto, Timeout: time.Second}
			if resp, _, err = c.Exchange(q, server); err == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err != nil || len(resp.Answer) != 2 {
			t.Fatalf("%s: %v %v", proto, err, resp)
		}
	}
	// A new view is served without restarting the listener.
	if err := l.Serve("", "127.0.0.1", &manager.DNSView{}); err != nil {
		t.Fatal(err)
	}
	if resp, _, err := (&dns.Client{Timeout: time.Second}).Exchange(q, server); err != nil || resp.Rcode != dns.RcodeRefused {
		t.Fatalf("after the swap: %v %v", err, resp)
	}
}

type dnsEnv struct {
	zones   *manager.DNSZoneRegistry
	records *manager.DNSRecordRegistry
	vpcs    *manager.VPCRegistry
}

func newDNSEnv(t *testing.T) *dnsEnv {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	return &dnsEnv{
		zones:   registry.New[resource.DNSZoneSpec, resource.DNSZoneStatus](store, resource.KindDNSZone),
		records: registry.New[resource.DNSRecordSpec, resource.DNSRecordStatus](store, resource.KindDNSRecord),
		vpcs:    registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC),
	}
}

func (env *dnsEnv) putVPC(t *testing.T, uid, cidr, bridge string) {
	t.Helper()
	v := &resource.VPC{Metadata: resource.ObjectMeta{UID: uid, Generation: 1}, Spec: resource.VPCSpec{CIDR: cidr}}
	v.Status.BridgeName = bridge
	if err := env.vpcs.Put(v); err != nil {
		t.Fatalf("seed vpc: %v", err)
	}
}

func (env *dnsEnv) putZone(t *testing.T, uid, domain string, vpcIDs ...string) {
	t.Helper()
	if err := env.zones.Put(&resource.DNSZone{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.DNSZoneSpec{Domain: domain, Visibility: "private", VPCIDs: vpcIDs},
	}); err != nil {
		t.Fatalf("seed zone: %v", err)
	}
}

func (env *dnsEnv) putRecord(t *testing.T, uid, zoneID, name, typ string, values ...string) {
	t.Helper()
	if err := env.records.Put(&resource.DNSRecord{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.DNSRecordSpec{ZoneID: zoneID, Name: name, Type: typ, Records: values},
	}); err != nil {
		t.Fatalf("seed record: %v", err)
	}
}

func (env *dnsEnv) putPublicZone(t *testing.T, uid, domain string) {
	t.Helper()
	if err := env.zones.Put(&resource.DNSZone{
		Metadata: resource.ObjectMeta{UID: uid, Generation: 1},
		Spec:     resource.DNSZoneSpec{Domain: domain, Visibility: "public"},
	}); err != nil {
		t.Fatalf("seed zone: %v", err)
	}
}
