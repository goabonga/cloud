// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestListenerSpecsSatisfyValidatorAndDefaulter(t *testing.T) {
	t.Parallel()

	var (
		_ resource.Validator                             = resource.LBTargetGroupSpec{}
		_ resource.Validator                             = resource.LBTargetSpec{}
		_ resource.Validator                             = resource.LBListenerSpec{}
		_ resource.Defaulter[resource.LBTargetGroupSpec] = resource.LBTargetGroupSpec{}
		_ resource.Defaulter[resource.LBTargetSpec]      = resource.LBTargetSpec{}
		_ resource.Defaulter[resource.LBListenerSpec]    = resource.LBListenerSpec{}
	)
}

func TestTargetGroupDefaults(t *testing.T) {
	t.Parallel()

	got := resource.LBTargetGroupSpec{VPCID: "vpc-1", Port: 8080}.WithDefaults()
	want := resource.LBHealthCheck{Protocol: "http", Path: "/", IntervalSeconds: 5, TimeoutSeconds: 2, HealthyThreshold: 2, UnhealthyThreshold: 3}
	if got.Protocol != "http" || got.HealthCheck != want {
		t.Fatalf("defaults = %+v", got)
	}
	tcp := resource.LBTargetGroupSpec{VPCID: "vpc-1", Port: 5432, Protocol: "tcp"}.WithDefaults()
	if tcp.HealthCheck.Protocol != "tcp" || tcp.HealthCheck.Path != "" {
		t.Fatalf("tcp health check = %+v, want tcp without path", tcp.HealthCheck)
	}
}

func TestListenerDefaults(t *testing.T) {
	t.Parallel()

	for proto, mode := range map[string]string{"https": "terminate", "tls": "passthrough", "http": "", "tcp": ""} {
		if got := (resource.LBListenerSpec{Protocol: proto}).WithDefaults().TLSMode; got != mode {
			t.Errorf("%s tlsMode = %q, want %q", proto, got, mode)
		}
	}
	if got := (resource.LBTargetSpec{}).WithDefaults().Weight; got != 1 {
		t.Fatalf("target weight = %d, want 1", got)
	}
}

func TestListenerModelValidate(t *testing.T) {
	t.Parallel()

	tg := func(f func(*resource.LBTargetGroupSpec)) resource.LBTargetGroupSpec {
		s := resource.LBTargetGroupSpec{VPCID: "vpc-1", Port: 8443}
		f(&s)
		return s
	}
	ln := func(f func(*resource.LBListenerSpec)) resource.LBListenerSpec {
		s := resource.LBListenerSpec{LoadBalancerID: "lb-1", Port: 443, Protocol: "https", CertificateIDs: []string{"c-1"}, DefaultTargetGroupID: "tg-1"}
		f(&s)
		return s
	}
	rule := func(host, path string) []resource.LBListenerRule {
		return []resource.LBListenerRule{{Host: host, PathPrefix: path, TargetGroupID: "tg-2"}}
	}
	tests := []struct {
		name    string
		spec    resource.Validator
		wantErr bool
	}{
		{"target group ok", tg(func(*resource.LBTargetGroupSpec) {}), false},
		{"target group https with ca", tg(func(s *resource.LBTargetGroupSpec) { s.Protocol, s.BackendCAID = "https", "ca-1" }), false},
		{"target group ca without https", tg(func(s *resource.LBTargetGroupSpec) { s.BackendCAID = "ca-1" }), true},
		{"target group https with server name", tg(func(s *resource.LBTargetGroupSpec) { s.Protocol, s.ServerName = "https", "web.internal.demo" }), false},
		{"target group server name without https", tg(func(s *resource.LBTargetGroupSpec) { s.ServerName = "web.internal.demo" }), true},
		{"target group no vpc", tg(func(s *resource.LBTargetGroupSpec) { s.VPCID = "" }), true},
		{"target group no port", tg(func(s *resource.LBTargetGroupSpec) { s.Port = 0 }), true},
		{"target group bad protocol", tg(func(s *resource.LBTargetGroupSpec) { s.Protocol = "udp" }), true},
		{"target group tcp check with path", tg(func(s *resource.LBTargetGroupSpec) {
			s.HealthCheck = resource.LBHealthCheck{Protocol: "tcp", Path: "/"}
		}), true},
		{"target group relative path", tg(func(s *resource.LBTargetGroupSpec) { s.HealthCheck.Path = "healthz" }), true},
		{"target group timeout over interval", tg(func(s *resource.LBTargetGroupSpec) {
			s.HealthCheck.IntervalSeconds, s.HealthCheck.TimeoutSeconds = 2, 2
		}), true},
		{"target group threshold too high", tg(func(s *resource.LBTargetGroupSpec) { s.HealthCheck.HealthyThreshold = 11 }), true},
		{"target ok", resource.LBTargetSpec{TargetGroupID: "tg-1", ComputeID: "i-1"}, false},
		{"target no compute", resource.LBTargetSpec{TargetGroupID: "tg-1"}, true},
		{"target bad weight", resource.LBTargetSpec{TargetGroupID: "tg-1", ComputeID: "i-1", Weight: 1001}, true},
		{"https listener ok", ln(func(*resource.LBListenerSpec) {}), false},
		{"https reencrypt with rules", ln(func(s *resource.LBListenerSpec) { s.TLSMode, s.Rules = "reencrypt", rule("api.demo.test", "/v1") }), false},
		{"https without certificate", ln(func(s *resource.LBListenerSpec) { s.CertificateIDs = nil }), true},
		{"https passthrough", ln(func(s *resource.LBListenerSpec) { s.TLSMode = "passthrough" }), true},
		{"http listener ok", ln(func(s *resource.LBListenerSpec) { s.Protocol, s.CertificateIDs = "http", nil }), false},
		{"http with certificate", ln(func(s *resource.LBListenerSpec) { s.Protocol = "http" }), true},
		{"tls passthrough by sni", ln(func(s *resource.LBListenerSpec) {
			s.Protocol, s.CertificateIDs, s.Rules = "tls", nil, rule("db.demo.test", "")
		}), false},
		{"tls with path rule", ln(func(s *resource.LBListenerSpec) { s.Protocol, s.CertificateIDs, s.Rules = "tls", nil, rule("", "/x") }), true},
		{"tls terminate", ln(func(s *resource.LBListenerSpec) { s.Protocol, s.CertificateIDs, s.TLSMode = "tls", nil, "terminate" }), true},
		{"tcp ok", ln(func(s *resource.LBListenerSpec) { s.Protocol, s.CertificateIDs = "tcp", nil }), false},
		{"tcp with rules", ln(func(s *resource.LBListenerSpec) { s.Protocol, s.CertificateIDs, s.Rules = "tcp", nil, rule("x", "") }), true},
		{"listener no default group", ln(func(s *resource.LBListenerSpec) { s.DefaultTargetGroupID = "" }), true},
		{"listener bad protocol", ln(func(s *resource.LBListenerSpec) { s.Protocol = "quic" }), true},
		{"rule without match", ln(func(s *resource.LBListenerSpec) { s.Rules = rule("", "") }), true},
		{"rule relative path", ln(func(s *resource.LBListenerSpec) { s.Rules = rule("", "v1") }), true},
		{"rule host with port", ln(func(s *resource.LBListenerSpec) { s.Rules = rule("a.test:443", "") }), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.spec.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
