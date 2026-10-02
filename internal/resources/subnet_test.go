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

func TestSubnetResourceMetadataAndSchema(t *testing.T) {
	t.Parallel()

	var meta resource.MetadataResponse
	NewSubnetResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "infra"}, &meta)
	if meta.TypeName != "infra_subnet" {
		t.Fatalf("TypeName = %q, want infra_subnet", meta.TypeName)
	}

	var sresp resource.SchemaResponse
	NewSubnetResource().Schema(context.Background(), resource.SchemaRequest{}, &sresp)
	for _, required := range []string{"vpc_id", "cidr"} {
		if !sresp.Schema.Attributes[required].IsRequired() {
			t.Fatalf("%s should be required", required)
		}
	}
	for _, computed := range []string{"id", "gateway", "phase"} {
		if !sresp.Schema.Attributes[computed].IsComputed() {
			t.Fatalf("%s should be computed", computed)
		}
	}
}

func TestSubnetModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewSubnetResource().(*genericResource[subnetModel, infra.SubnetSpec, infra.SubnetStatus]).def
	spec := infra.SubnetSpec{VPCID: "vpc-1", CIDR: "10.0.1.0/24", Type: "private"}
	r := &infra.Subnet{Metadata: infra.ObjectMeta{UID: "subnet-1"}, Spec: spec}
	r.Status.Gateway = "10.0.1.1"
	r.Status.Phase = infra.PhaseReady

	m, diags := def.toModel(context.Background(), r)
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if m.Gateway.ValueString() != "10.0.1.1" {
		t.Fatalf("gateway = %q, want 10.0.1.1", m.Gateway.ValueString())
	}

	got, diags := def.toSpec(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("round trip = %+v, want %+v", got, spec)
	}
	if def.id(m) != "subnet-1" {
		t.Fatalf("id() = %q, want subnet-1", def.id(m))
	}
}
