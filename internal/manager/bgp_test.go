// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/goabonga/infrastructure/internal/manager"
)

type staticSource []string

func (s staticSource) PublicAddresses() []string { return s }

type recordingSpeaker struct{ announced []netip.Prefix }

func (r *recordingSpeaker) Announce(_ context.Context, p []netip.Prefix) error {
	r.announced = p
	return nil
}

func TestBGPAnnouncesEveryServedAddressOnce(t *testing.T) {
	t.Parallel()

	sp := &recordingSpeaker{}
	r := manager.NewBGPReconciler(sp, staticSource{"203.0.113.53"}, staticSource{"203.0.113.11", "203.0.113.10", "203.0.113.53"})
	if err := r.ReconcileAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("203.0.113.10/32"),
		netip.MustParsePrefix("203.0.113.11/32"),
		netip.MustParsePrefix("203.0.113.53/32"),
	}
	if !slices.Equal(sp.announced, want) {
		t.Fatalf("announced %v, want %v", sp.announced, want)
	}
	if err := manager.NewBGPReconciler(sp, staticSource{"not-an-ip"}).ReconcileAll(context.Background()); err == nil {
		t.Fatal("a bad address must fail the pass")
	}
}

func TestLoadBalancerAnnouncesOnlyWhatItServes(t *testing.T) {
	t.Parallel()

	env := newLBEnv(t)
	env.putResolvedPublicIP(t, "pub", "203.0.113.10")
	env.putPublicLB(t, "lb", "10.0.0.10", "pub")
	r := env.reconciler(&serviceLog{}).AsEdge(env.ips)
	if err := r.Reconcile(context.Background(), "lb"); err != nil {
		t.Fatal(err)
	}
	if got := r.PublicAddresses(); !slices.Equal(got, []string{"203.0.113.10"}) {
		t.Fatalf("served %v", got)
	}

	// The next pass fails to serve it: it is no longer announced.
	failing := env.reconciler(&failingPublic{}).AsEdge(env.ips)
	_ = failing.Reconcile(context.Background(), "lb")
	if got := failing.PublicAddresses(); len(got) != 0 {
		t.Fatalf("a public address that failed must not be announced: %v", got)
	}
}

// failingPublic serves the VIP but fails every public address.
type failingPublic struct{ serviceLog }

func (f *failingPublic) EnsurePublicService(context.Context, string, string, int, string, string, []manager.LBRealServer) error {
	return errors.New("public leg down")
}
