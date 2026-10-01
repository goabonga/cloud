// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/osrg/gobgp/v4/api"
	"github.com/osrg/gobgp/v4/pkg/apiutil"
	"github.com/osrg/gobgp/v4/pkg/packet/bgp"
	"github.com/osrg/gobgp/v4/pkg/server"
)

// fakeUpstream is a GoBGP router on loopback, waiting for the edge's session
// on port, and accepting what it announces.
func fakeUpstream(t *testing.T, port uint16) *server.BgpServer {
	t.Helper()
	ctx := context.Background()
	up := server.NewBgpServer(server.LoggerOption(slog.New(slog.DiscardHandler), &slog.LevelVar{}))
	go up.Serve()
	t.Cleanup(up.Stop)
	if err := up.StartBgp(ctx, &api.StartBgpRequest{Global: &api.Global{
		Asn: 65000, RouterId: "192.0.2.100", ListenPort: int32(port), ListenAddresses: []string{"127.0.0.1"},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := up.AddPeer(ctx, &api.AddPeerRequest{Peer: &api.Peer{
		Conf:      &api.PeerConf{NeighborAddress: "127.0.0.1", PeerAsn: 65010},
		Transport: &api.Transport{PassiveMode: true},
	}}); err != nil {
		t.Fatal(err)
	}
	return up
}

// learned returns the IPv4 prefixes in the upstream's RIB.
func learned(up *server.BgpServer) []string {
	var out []string
	_ = up.ListPath(apiutil.ListPathRequest{TableType: api.TableType_TABLE_TYPE_GLOBAL, Family: bgp.RF_IPv4_UC},
		func(prefix bgp.NLRI, _ []*apiutil.Path) { out = append(out, prefix.String()) })
	slices.Sort(out)
	return out
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func freePort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return uint16(l.Addr().(*net.TCPAddr).Port) // #nosec G115 -- a TCP port fits 16 bits
}

func TestGoBGPSpeakerAnnouncesAndWithdraws(t *testing.T) {
	t.Parallel()

	port := freePort(t)
	up := fakeUpstream(t, port)
	ctx := context.Background()
	sp, err := NewGoBGPSpeaker(ctx, BGPConfig{
		ASN: 65010, RouterID: netip.MustParseAddr("192.0.2.1"),
		Peers: []BGPPeer{{Address: netip.MustParseAddr("127.0.0.1"), ASN: 65000, Port: port}},
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}

	a, b := netip.MustParsePrefix("203.0.113.10/32"), netip.MustParsePrefix("203.0.113.53/32")
	if err := sp.Announce(ctx, []netip.Prefix{a, b}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "both prefixes", func() bool {
		return slices.Equal(learned(up), []string{"203.0.113.10/32", "203.0.113.53/32"})
	})
	// Idempotent, then a withdrawal.
	if err := sp.Announce(ctx, []netip.Prefix{a, b}); err != nil {
		t.Fatal(err)
	}
	if err := sp.Announce(ctx, []netip.Prefix{b}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the withdrawal", func() bool { return slices.Equal(learned(up), []string{"203.0.113.53/32"}) })

	// Stopping the speaker takes its routes away at once.
	if err := sp.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the routes to go with the speaker", func() bool { return len(learned(up)) == 0 })
}

func TestParseBGPPeers(t *testing.T) {
	t.Parallel()

	peers, err := ParseBGPPeers("10.99.0.100@65000, 10.99.0.101@65001")
	if err != nil || len(peers) != 2 || peers[1].Address != netip.MustParseAddr("10.99.0.101") || peers[1].ASN != 65001 {
		t.Fatalf("peers %+v, %v", peers, err)
	}
	for _, bad := range []string{"10.99.0.100", "nope@65000", "10.99.0.100@0", "10.99.0.100@x"} {
		if _, err := ParseBGPPeers(bad); err == nil {
			t.Fatalf("%q must not parse", bad)
		}
	}
}
