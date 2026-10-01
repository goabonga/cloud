// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/osrg/gobgp/v4/api"
	"github.com/osrg/gobgp/v4/pkg/apiutil"
	"github.com/osrg/gobgp/v4/pkg/packet/bgp"
	"github.com/osrg/gobgp/v4/pkg/server"
)

// BGPPeer is an upstream router the edge peers with.
type BGPPeer struct {
	Address netip.Addr
	ASN     uint32
	// Port is the peer's BGP port; 0 means 179.
	Port uint16
}

// BGPConfig configures the edge's BGP speaker.
type BGPConfig struct {
	ASN      uint32
	RouterID netip.Addr
	Peers    []BGPPeer
	// HoldTime is the session hold time in seconds, KeepaliveInterval a third
	// of it. 0 means 3: a host that dies takes its routes with it within 3s
	// even without BFD.
	HoldTime uint64
	// BFD detects a dead peer within a second, on UDP 3784.
	BFD bool
}

// ParseBGPPeers parses "addr@asn[,addr@asn...]", e.g. "10.99.0.100@65000".
func ParseBGPPeers(s string) ([]BGPPeer, error) {
	var peers []BGPPeer
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		addr, asn, ok := strings.Cut(item, "@")
		if !ok {
			return nil, fmt.Errorf("manager: bgp peer %q is not addr@asn", item)
		}
		a, err := netip.ParseAddr(addr)
		if err != nil {
			return nil, fmt.Errorf("manager: bgp peer %q: %w", item, err)
		}
		n, err := strconv.ParseUint(asn, 10, 32)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("manager: bgp peer %q: bad asn", item)
		}
		peers = append(peers, BGPPeer{Address: a, ASN: uint32(n)})
	}
	return peers, nil
}

// GoBGPSpeaker is the BGPSpeaker embedding GoBGP. It never listens: it opens
// the sessions to its peers itself. What a peer sends it stays in its RIB:
// it runs without zebra, so nothing reaches the host's routing tables (GoBGP's
// global import policy would reject the edge's own paths too).
type GoBGPSpeaker struct {
	srv *server.BgpServer

	mu        sync.Mutex
	announced map[netip.Prefix]uuid.UUID
}

// NewGoBGPSpeaker starts the speaker and its sessions to cfg.Peers.
func NewGoBGPSpeaker(ctx context.Context, cfg BGPConfig, logger *slog.Logger) (*GoBGPSpeaker, error) {
	if cfg.ASN == 0 || !cfg.RouterID.Is4() || len(cfg.Peers) == 0 {
		return nil, fmt.Errorf("manager: bgp needs an asn, an IPv4 router id and a peer")
	}
	hold := cfg.HoldTime
	if hold == 0 {
		hold = 3
	}
	lvl := &slog.LevelVar{}
	lvl.Set(slog.LevelWarn)
	srv := server.NewBgpServer(server.LoggerOption(logger, lvl))
	go srv.Serve()
	if err := srv.StartBgp(ctx, &api.StartBgpRequest{Global: &api.Global{
		Asn:        cfg.ASN,
		RouterId:   cfg.RouterID.String(),
		ListenPort: -1,
	}}); err != nil {
		srv.Stop()
		return nil, fmt.Errorf("manager: start bgp: %w", err)
	}
	for _, p := range cfg.Peers {
		peer := &api.Peer{
			Conf: &api.PeerConf{NeighborAddress: p.Address.String(), PeerAsn: p.ASN},
			// Redial within a second after any reset: the speaker does not
			// listen, so the upstream cannot reopen the session itself.
			Timers: &api.Timers{Config: &api.TimersConfig{
				HoldTime:               hold,
				KeepaliveInterval:      max(hold/3, 1),
				ConnectRetry:           1,
				IdleHoldTimeAfterReset: 1,
			}},
			AfiSafis: []*api.AfiSafi{{Config: &api.AfiSafiConfig{
				Family:  &api.Family{Afi: api.Family_AFI_IP, Safi: api.Family_SAFI_UNICAST},
				Enabled: true,
			}}},
		}
		if p.Port != 0 {
			peer.Transport = &api.Transport{RemotePort: uint32(p.Port)}
		}
		if cfg.BFD {
			peer.Bfd = &api.BfdPeerConfig{
				Enabled:                  true,
				DesiredMinimumTxInterval: 300000,
				RequiredMinimumReceive:   300000,
				DetectionMultiplier:      3,
			}
		}
		if err := srv.AddPeer(ctx, &api.AddPeerRequest{Peer: peer}); err != nil {
			srv.Stop()
			return nil, fmt.Errorf("manager: bgp peer %s: %w", p.Address, err)
		}
	}
	return &GoBGPSpeaker{srv: srv, announced: map[netip.Prefix]uuid.UUID{}}, nil
}

// Announce implements BGPSpeaker: it adds the missing prefixes, next hop
// self, and withdraws the others.
func (s *GoBGPSpeaker) Announce(_ context.Context, prefixes []netip.Prefix) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	want := map[netip.Prefix]bool{}
	for _, p := range prefixes {
		want[p] = true
		if _, ok := s.announced[p]; ok {
			continue
		}
		path, err := selfPath(p)
		if err != nil {
			return err
		}
		resp, err := s.srv.AddPath(apiutil.AddPathRequest{Paths: []*apiutil.Path{path}})
		if err == nil && len(resp) == 1 && resp[0].Error != nil {
			err = resp[0].Error
		}
		if err != nil || len(resp) != 1 {
			return fmt.Errorf("manager: announce %s: %w", p, err)
		}
		s.announced[p] = resp[0].UUID
	}
	for p, id := range s.announced {
		if want[p] {
			continue
		}
		if err := s.srv.DeletePath(apiutil.DeletePathRequest{UUIDs: []uuid.UUID{id}}); err != nil {
			return fmt.Errorf("manager: withdraw %s: %w", p, err)
		}
		delete(s.announced, p)
	}
	return nil
}

// Stop withdraws every route by closing the sessions, so the upstream stops
// sending traffic here at once rather than when the hold timer runs out.
func (s *GoBGPSpeaker) Stop(ctx context.Context) error {
	err := s.srv.StopBgp(ctx, &api.StopBgpRequest{})
	s.srv.Stop()
	return err
}

// selfPath is the path announcing p with this host as the next hop: GoBGP
// replaces the unspecified next hop with the session's local address.
func selfPath(p netip.Prefix) (*apiutil.Path, error) {
	nlri, err := bgp.NewIPAddrPrefix(p)
	if err != nil {
		return nil, fmt.Errorf("manager: bgp prefix %s: %w", p, err)
	}
	nh, err := bgp.NewPathAttributeNextHop(netip.IPv4Unspecified())
	if err != nil {
		return nil, fmt.Errorf("manager: bgp next hop: %w", err)
	}
	return &apiutil.Path{
		Family: bgp.RF_IPv4_UC,
		Nlri:   nlri,
		Attrs:  []bgp.PathAttributeInterface{bgp.NewPathAttributeOrigin(bgp.BGP_ORIGIN_ATTR_TYPE_IGP), nh},
	}, nil
}
