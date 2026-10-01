// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestNetworkResourcesMetadataAndSchema(t *testing.T) {
	t.Parallel()

	cases := []struct {
		factory  func() resource.Resource
		wantType string
		required []string
	}{
		{NewIPAddressResource, "infra_ip_address", nil},
		{NewIGWResource, "infra_igw", []string{"vpc_id"}},
		{NewRouteResource, "infra_route", []string{"vpc_id", "destination", "gateway"}},
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

func TestIPAddressModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewIPAddressResource().(*genericResource[ipAddressModel, infra.IPAddressSpec, infra.IPAddressStatus]).def
	spec := infra.IPAddressSpec{Type: "public", SubnetID: "subnet-1", VPCID: "vpc-1", ComputeID: "compute-1"}
	r := &infra.IPAddress{Metadata: infra.ObjectMeta{UID: "ip-1"}, Spec: spec}
	r.Status.Address = "203.0.113.5"
	r.Status.Phase = infra.PhaseReady

	m, diags := def.toModel(context.Background(), r)
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if m.Address.ValueString() != "203.0.113.5" {
		t.Fatalf("address = %q, want the status address when set", m.Address.ValueString())
	}

	got, diags := def.toSpec(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	// toSpec reads the address back from the model, which read the status's
	// resolved address rather than the spec's requested one.
	want := spec
	want.Address = "203.0.113.5"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	if def.id(m) != "ip-1" {
		t.Fatalf("id() = %q, want ip-1", def.id(m))
	}
}

func TestIPAddressModelFallsBackToTheRequestedAddress(t *testing.T) {
	t.Parallel()

	def := NewIPAddressResource().(*genericResource[ipAddressModel, infra.IPAddressSpec, infra.IPAddressStatus]).def
	spec := infra.IPAddressSpec{Type: "private", Address: "10.0.0.9"}
	m, diags := def.toModel(context.Background(), &infra.IPAddress{Metadata: infra.ObjectMeta{UID: "ip-2"}, Spec: spec})
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if m.Address.ValueString() != "10.0.0.9" {
		t.Fatalf("address = %q, want the spec's requested address when the status has none", m.Address.ValueString())
	}
}

func TestIGWModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewIGWResource().(*genericResource[igwModel, infra.IGWSpec, infra.IGWStatus]).def
	spec := infra.IGWSpec{VPCID: "vpc-1"}
	r := &infra.IGW{Metadata: infra.ObjectMeta{UID: "igw-1"}, Spec: spec}
	r.Status.HostIface = "eth0"
	r.Status.Bridge = "br-vpc0"
	r.Status.Phase = infra.PhaseReady

	m, diags := def.toModel(context.Background(), r)
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if !m.EgressProxy.IsNull() {
		t.Fatal("no egress proxy must read back as null")
	}
	if m.EgressProxyAddress.ValueString() != "" {
		t.Fatalf("egress_proxy_address = %q, want empty without a proxy", m.EgressProxyAddress.ValueString())
	}

	got, diags := def.toSpec(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("round trip = %+v, want %+v", got, spec)
	}
	if def.id(m) != "igw-1" {
		t.Fatalf("id() = %q, want igw-1", def.id(m))
	}
}

func TestIGWModelWithEgressProxyRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewIGWResource().(*genericResource[igwModel, infra.IGWSpec, infra.IGWStatus]).def
	spec := infra.IGWSpec{
		VPCID: "vpc-1",
		EgressProxy: &infra.EgressProxySpec{
			Enabled:        true,
			AllowedDomains: []string{"example.com"},
		},
	}
	r := &infra.IGW{Metadata: infra.ObjectMeta{UID: "igw-1"}, Spec: spec}
	r.Status.EgressProxy = &infra.EgressProxyStatus{Address: "10.0.0.2"}
	r.Status.Phase = infra.PhaseReady

	m, diags := def.toModel(context.Background(), r)
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if m.EgressProxyAddress.ValueString() != "10.0.0.2" {
		t.Fatalf("egress_proxy_address = %q, want 10.0.0.2", m.EgressProxyAddress.ValueString())
	}

	got, diags := def.toSpec(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("round trip = %+v, want %+v", got, spec)
	}
}

func TestRouteModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewRouteResource().(*genericResource[routeModel, infra.RouteSpec, infra.RouteStatus]).def
	spec := infra.RouteSpec{VPCID: "vpc-1", SubnetID: "subnet-1", Destination: "0.0.0.0/0", Gateway: "igw-1"}
	m, diags := def.toModel(context.Background(), &infra.Route{Metadata: infra.ObjectMeta{UID: "route-1"}, Spec: spec})
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
	if def.id(m) != "route-1" {
		t.Fatalf("id() = %q, want route-1", def.id(m))
	}
}

func TestNullableHelpers(t *testing.T) {
	t.Parallel()

	if !nullableString("").IsNull() || nullableString("x") != types.StringValue("x") {
		t.Fatal("nullableString must read an empty string back as null")
	}
	if !nullableInt(0).IsNull() || nullableInt(7) != types.Int64Value(7) {
		t.Fatal("nullableInt must read a zero value back as null")
	}
}
