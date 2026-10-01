// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"
	"reflect"
	"testing"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestTargetGroupModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewLBTargetGroupResource().(*genericResource[lbTargetGroupModel, infra.LBTargetGroupSpec, infra.LBTargetGroupStatus]).def
	spec := infra.LBTargetGroupSpec{VPCID: "vpc-1", Port: 8443, Protocol: "https", BackendCAID: "ca-1"}.WithDefaults()
	m, diags := def.toModel(context.Background(), &infra.LBTargetGroup{Metadata: infra.ObjectMeta{UID: "tg-1"}, Spec: spec})
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	got, diags := def.toSpec(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("round trip = %+v, want %+v", got, spec)
	}
	if def.id(m) != "tg-1" {
		t.Fatalf("id() = %q, want tg-1", def.id(m))
	}
}

func TestLBTargetModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewLBTargetResource().(*genericResource[lbTargetModel, infra.LBTargetSpec, infra.LBTargetStatus]).def
	spec := infra.LBTargetSpec{TargetGroupID: "tg-1", ComputeID: "compute-1", Port: 8080, Weight: 5}
	m, diags := def.toModel(context.Background(), &infra.LBTarget{Metadata: infra.ObjectMeta{UID: "target-1"}, Spec: spec})
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	got, diags := def.toSpec(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("round trip = %+v, want %+v", got, spec)
	}
	if def.id(m) != "target-1" {
		t.Fatalf("id() = %q, want target-1", def.id(m))
	}
}

func TestListenerModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewLBListenerResource().(*genericResource[lbListenerModel, infra.LBListenerSpec, infra.LBListenerStatus]).def
	spec := infra.LBListenerSpec{
		LoadBalancerID: "lb-1", Port: 443, Protocol: "https", TLSMode: "reencrypt",
		CertificateIDs:       []string{"cert-www", "cert-api"},
		DefaultTargetGroupID: "tg-web",
		Rules: []infra.LBListenerRule{
			{Host: "api.demo.test", PathPrefix: "/v1", TargetGroupID: "tg-api"},
			{Host: "static.demo.test", TargetGroupID: "tg-static"},
		},
	}
	m, diags := def.toModel(context.Background(), &infra.LBListener{Metadata: infra.ObjectMeta{UID: "l-1"}, Spec: spec})
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	got, diags := def.toSpec(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("round trip = %+v, want %+v", got, spec)
	}
	if def.id(m) != "l-1" {
		t.Fatalf("id() = %q, want l-1", def.id(m))
	}
}
