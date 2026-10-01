// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestSecurityGroupResourcesMetadataAndSchema(t *testing.T) {
	t.Parallel()

	cases := []struct {
		factory  func() resource.Resource
		wantType string
		required []string
	}{
		{NewSecurityGroupResource, "infra_security_group", []string{"vpc_id"}},
		{NewSecurityGroupRuleResource, "infra_security_group_rule", []string{"security_group_id", "direction", "protocol"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.wantType, func(t *testing.T) {
			t.Parallel()
			r := tc.factory()

			var meta resource.MetadataResponse
			r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "infra"}, &meta)
			if meta.TypeName != tc.wantType {
				t.Fatalf("TypeName = %q, want %q", meta.TypeName, tc.wantType)
			}

			var sresp resource.SchemaResponse
			r.Schema(context.Background(), resource.SchemaRequest{}, &sresp)
			for _, name := range tc.required {
				if !sresp.Schema.Attributes[name].IsRequired() {
					t.Fatalf("%s should be required", name)
				}
			}
			for _, computed := range []string{"id", "phase"} {
				if !sresp.Schema.Attributes[computed].IsComputed() {
					t.Fatalf("%s should be computed", computed)
				}
			}
		})
	}
}

func TestSecurityGroupModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewSecurityGroupResource().(*genericResource[securityGroupModel, infra.SecurityGroupSpec, infra.SecurityGroupStatus]).def
	spec := infra.SecurityGroupSpec{VPCID: "vpc-1", Name: "web"}
	r := &infra.SecurityGroup{Metadata: infra.ObjectMeta{UID: "sg-1"}, Spec: spec}
	r.Status.Chain = "infra-sg-1"
	r.Status.Phase = infra.PhaseReady

	m, diags := def.toModel(context.Background(), r)
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
	if def.id(m) != "sg-1" {
		t.Fatalf("id() = %q, want sg-1", def.id(m))
	}
}

func TestSecurityGroupRuleModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewSecurityGroupRuleResource().(*genericResource[securityGroupRuleModel, infra.SecurityGroupRuleSpec, infra.SecurityGroupRuleStatus]).def
	spec := infra.SecurityGroupRuleSpec{
		SecurityGroupID: "sg-1", Direction: "ingress", Protocol: "tcp", Port: 443, CIDR: "0.0.0.0/0",
	}
	m, diags := def.toModel(context.Background(), &infra.SecurityGroupRule{Metadata: infra.ObjectMeta{UID: "rule-1"}, Spec: spec})
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
