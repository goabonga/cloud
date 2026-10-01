// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
)

// An edge announces the public addresses it serves to the upstream over BGP,
// each as a /32 with itself as the next hop, and withdraws one as soon as it
// stops serving it. The upstream spreads the traffic for an address over the
// edges announcing it and drops an edge the moment its routes go - when the
// address stops being served, when the agent stops, or when the session dies
// with the host - instead of sending flows into a dead edge as a static route
// would.

// PublicSource reports the public addresses a pass serves on this host.
type PublicSource interface {
	PublicAddresses() []string
}

// BGPSpeaker announces prefixes to the upstream.
type BGPSpeaker interface {
	// Announce makes the announced prefixes exactly prefixes. Idempotent.
	Announce(ctx context.Context, prefixes []netip.Prefix) error
}

// BGPReconciler announces what its sources serve. It runs after them in the
// agent's loop, so it announces what this pass actually served.
type BGPReconciler struct {
	speaker BGPSpeaker
	sources []PublicSource
}

// NewBGPReconciler returns a BGP pass announcing the addresses of sources.
func NewBGPReconciler(speaker BGPSpeaker, sources ...PublicSource) *BGPReconciler {
	return &BGPReconciler{speaker: speaker, sources: sources}
}

// Name identifies the reconcile pass.
func (r *BGPReconciler) Name() string { return "bgp" }

// ReconcileAll announces every served public address as a /32.
func (r *BGPReconciler) ReconcileAll(ctx context.Context) error {
	var prefixes []netip.Prefix
	for _, src := range r.sources {
		for _, a := range src.PublicAddresses() {
			addr, err := netip.ParseAddr(a)
			if err != nil || !addr.Is4() {
				return fmt.Errorf("manager: bgp: %q is not an IPv4 address", a)
			}
			if p := netip.PrefixFrom(addr, 32); !slices.Contains(prefixes, p) {
				prefixes = append(prefixes, p)
			}
		}
	}
	slices.SortFunc(prefixes, func(a, b netip.Prefix) int { return a.Addr().Compare(b.Addr()) })
	return r.speaker.Announce(ctx, prefixes)
}
