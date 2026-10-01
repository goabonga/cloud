// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package lbproxy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseConfigFillsDefaults(t *testing.T) {
	t.Parallel()

	cert := issue(t, newCA(t, "ca"), "demo.test")
	raw, _ := json.Marshal(Config{
		Listeners: []Listener{
			{Name: "web", Port: 443, Protocol: "HTTPS", Certificates: []Certificate{cert}, DefaultTargetGroup: "web"},
			{Name: "sni", Port: 8443, Protocol: "tls", DefaultTargetGroup: "raw"},
		},
		TargetGroups: []TargetGroup{
			{Name: "web", Protocol: "http", Targets: []Target{{Address: "10.0.0.1", Port: 80}}},
			{Name: "raw", Protocol: "tcp", Targets: []Target{{Address: "10.0.0.2", Port: 443, Weight: 5}}},
		},
	})
	cfg, err := ParseConfig(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Listeners[0].Protocol != ProtocolHTTPS || cfg.Listeners[0].TLSMode != TLSModeTerminate {
		t.Fatalf("https listener: %+v", cfg.Listeners[0])
	}
	if cfg.Listeners[1].TLSMode != TLSModePassthrough {
		t.Fatalf("tls listener mode %q", cfg.Listeners[1].TLSMode)
	}
	hc := cfg.TargetGroups[0].HealthCheck
	if hc.Protocol != "http" || hc.Path != "/" || hc.IntervalSeconds != 5 || hc.TimeoutSeconds != 2 ||
		hc.HealthyThreshold != 2 || hc.UnhealthyThreshold != 3 {
		t.Fatalf("health check defaults: %+v", hc)
	}
	if tg := cfg.TargetGroups[0].Targets[0]; tg.Weight != 1 || tg.ID != "10.0.0.1:80" {
		t.Fatalf("target defaults: %+v", tg)
	}
	if hc := cfg.TargetGroups[1].HealthCheck; hc.Protocol != "tcp" || hc.Path != "" {
		t.Fatalf("tcp health check: %+v", hc)
	}
}

func TestParseConfigRejectsInconsistencies(t *testing.T) {
	t.Parallel()

	cert := issue(t, newCA(t, "ca"), "demo.test")
	web := TargetGroup{Name: "web", Protocol: "http", Targets: []Target{{Address: "10.0.0.1", Port: 80}}}
	tls := TargetGroup{Name: "tls", Protocol: "https", Targets: []Target{{Address: "10.0.0.1", Port: 443}}}
	for _, tc := range []struct {
		name string
		cfg  Config
		want string
	}{
		{"unknown group", Config{Listeners: []Listener{{Name: "l", Port: 80, Protocol: "http", DefaultTargetGroup: "nope"}}, TargetGroups: []TargetGroup{web}}, "not declared"},
		{"terminate to https", Config{Listeners: []Listener{{Name: "l", Port: 443, Protocol: "https", Certificates: []Certificate{cert}, DefaultTargetGroup: "tls"}}, TargetGroups: []TargetGroup{tls}}, "speaks https"},
		{"reencrypt to http", Config{Listeners: []Listener{{Name: "l", Port: 443, Protocol: "https", TLSMode: "reencrypt", Certificates: []Certificate{cert}, DefaultTargetGroup: "web"}}, TargetGroups: []TargetGroup{web}}, "speaks http"},
		{"https without certificate", Config{Listeners: []Listener{{Name: "l", Port: 443, Protocol: "https", DefaultTargetGroup: "web"}}, TargetGroups: []TargetGroup{web}}, "needs a certificate"},
		{"http with certificate", Config{Listeners: []Listener{{Name: "l", Port: 80, Protocol: "http", Certificates: []Certificate{cert}, DefaultTargetGroup: "web"}}, TargetGroups: []TargetGroup{web}}, "only https"},
		{"bad key pair", Config{Listeners: []Listener{{Name: "l", Port: 443, Protocol: "https", Certificates: []Certificate{{CertPEM: cert.CertPEM, KeyPEM: "junk"}}, DefaultTargetGroup: "web"}}, TargetGroups: []TargetGroup{web}}, "certificate 0"},
		{"tls rule with path", Config{Listeners: []Listener{{Name: "l", Port: 443, Protocol: "tls", DefaultTargetGroup: "tls", Rules: []Rule{{PathPrefix: "/a", TargetGroup: "tls"}}}}, TargetGroups: []TargetGroup{tls}}, "SNI only"},
		{"tcp rule", Config{Listeners: []Listener{{Name: "l", Port: 22, Protocol: "tcp", DefaultTargetGroup: "raw", Rules: []Rule{{Host: "a", TargetGroup: "raw"}}}}, TargetGroups: []TargetGroup{{Name: "raw", Protocol: "tcp"}}}, "no rules"},
		{"empty rule", Config{Listeners: []Listener{{Name: "l", Port: 80, Protocol: "http", DefaultTargetGroup: "web", Rules: []Rule{{TargetGroup: "web"}}}}, TargetGroups: []TargetGroup{web}}, "host or a path"},
		{"duplicate listener", Config{Listeners: []Listener{{Name: "l", Port: 80, Protocol: "http", DefaultTargetGroup: "web"}, {Name: "l", Port: 81, Protocol: "http", DefaultTargetGroup: "web"}}, TargetGroups: []TargetGroup{web}}, "declared twice"},
		{"bad target", Config{TargetGroups: []TargetGroup{{Name: "g", Protocol: "http", Targets: []Target{{Address: "web-1", Port: 80}}}}}, "not an IP"},
		{"ca on http", Config{TargetGroups: []TargetGroup{{Name: "g", Protocol: "http", BackendCAPEM: cert.CertPEM}}}, "needs protocol https"},
		{"timeout over interval", Config{TargetGroups: []TargetGroup{{Name: "g", Protocol: "tcp", HealthCheck: HealthCheck{IntervalSeconds: 1, TimeoutSeconds: 3}}}}, "exceeds its interval"},
	} {
		raw, _ := json.Marshal(tc.cfg)
		if _, err := ParseConfig(raw); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
	if _, err := ParseConfig([]byte(`{"listeners": [], "bogus": 1}`)); err == nil {
		t.Error("an unknown field must be rejected")
	}
}

func TestRouteMatchesHostThenLongestPath(t *testing.T) {
	t.Parallel()

	l := &Listener{DefaultTargetGroup: "default", Rules: []Rule{
		{Host: "api.demo.test", TargetGroup: "api"},
		{PathPrefix: "/static", TargetGroup: "static"},
		{Host: "api.demo.test", PathPrefix: "/static", TargetGroup: "api-static"},
		{Host: "*.apps.test", TargetGroup: "apps"},
	}}
	for _, tc := range []struct{ host, path, want string }{
		{"api.demo.test", "/", "api"},
		{"API.demo.test:8443", "/v1", "api"},
		{"www.demo.test", "/static/app.js", "static"},
		{"api.demo.test", "/static/app.js", "api-static"},
		{"x.apps.test", "/", "apps"},
		{"apps.test", "/", "default"},
		{"www.demo.test", "/", "default"},
	} {
		if got := route(l, tc.host, tc.path); got != tc.want {
			t.Errorf("route(%q, %q) = %q, want %q", tc.host, tc.path, got, tc.want)
		}
	}
}

func TestPickIsWeightedAndFailsOpen(t *testing.T) {
	t.Parallel()

	g := newGroup(TargetGroup{Name: "g", Targets: []Target{
		{ID: "a", Address: "10.0.0.1", Port: 80, Weight: 3},
		{ID: "b", Address: "10.0.0.2", Port: 80, Weight: 1},
	}}, map[string]*targetState{}, testLogger())
	counts := map[string]int{}
	for range 8 {
		counts[g.pick(nil).ID]++
	}
	if counts["a"] != 6 || counts["b"] != 2 {
		t.Fatalf("weights 3:1 over 8 picks: %v", counts)
	}

	g.targets[0].state.observeCheck(errTest, 1, 1)
	for range 4 {
		if id := g.pick(nil).ID; id != "b" {
			t.Fatalf("an unhealthy target was picked: %s", id)
		}
	}
	g.targets[1].state.observeCheck(errTest, 1, 1)
	seen := map[string]bool{}
	for range 4 {
		seen[g.pick(nil).ID] = true
	}
	if !seen["a"] || !seen["b"] {
		t.Fatalf("with no healthy target, every target must be used: %v", seen)
	}
	if g.pick(map[*target]bool{g.targets[0]: true, g.targets[1]: true}) != nil {
		t.Fatal("excluding every target must leave nothing")
	}
}
