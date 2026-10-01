// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"
	"reflect"
	"testing"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestWAFPolicyModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewWAFPolicyResource().(*genericResource[wafPolicyModel, infra.WAFPolicySpec, infra.WAFPolicyStatus]).def
	spec := infra.WAFPolicySpec{Name: "edge", TargetType: "igw", TargetID: "igw-1", LogEnabled: true}
	r := &infra.WAFPolicy{Metadata: infra.ObjectMeta{UID: "policy-1"}, Spec: spec}
	r.Status.Chain = "infra-waf-1"
	r.Status.Phase = infra.PhaseReady

	m, diags := def.toModel(context.Background(), r)
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if m.Chain.ValueString() != "infra-waf-1" {
		t.Fatalf("chain = %q, want infra-waf-1", m.Chain.ValueString())
	}

	got, diags := def.toSpec(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("round trip = %+v, want %+v", got, spec)
	}
	if def.id(m) != "policy-1" {
		t.Fatalf("id() = %q, want policy-1", def.id(m))
	}
}

func TestWAFRuleModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewWAFRuleResource().(*genericResource[wafRuleModel, infra.WAFRuleSpec, infra.WAFRuleStatus]).def
	spec := infra.WAFRuleSpec{
		PolicyID: "policy-1", Priority: 10, MatchType: "path", MatchValue: "/admin",
		Action: "block", RateLimit: 100, RateWindow: 60,
	}
	m, diags := def.toModel(context.Background(), &infra.WAFRule{Metadata: infra.ObjectMeta{UID: "rule-1"}, Spec: spec})
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
	if def.id(m) != "rule-1" {
		t.Fatalf("id() = %q, want rule-1", def.id(m))
	}
}
