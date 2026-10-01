// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestNewID(t *testing.T) {
	t.Parallel()

	id := newID("vpc")
	if !strings.HasPrefix(id, "vpc-") {
		t.Fatalf("id %q missing kind prefix", id)
	}
	if len(id) != len("vpc-")+12 { // 6 random bytes -> 12 hex chars
		t.Fatalf("id %q has unexpected length %d", id, len(id))
	}
	if other := newID("vpc"); id == other {
		t.Fatal("newID should not repeat")
	}
}

func TestVPCResourceMetadata(t *testing.T) {
	t.Parallel()

	var resp resource.MetadataResponse
	NewVPCResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "infra"}, &resp)
	if resp.TypeName != "infra_vpc" {
		t.Fatalf("TypeName = %q, want infra_vpc", resp.TypeName)
	}
}

func TestVPCResourceSchema(t *testing.T) {
	t.Parallel()

	var resp resource.SchemaResponse
	NewVPCResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	attrs := resp.Schema.Attributes

	if !attrs["cidr"].IsRequired() {
		t.Fatal("cidr should be required")
	}
	for _, computed := range []string{"id", "bridge_name", "phase"} {
		if !attrs[computed].IsComputed() {
			t.Fatalf("%s should be computed", computed)
		}
	}
}

func TestEgressProxyRoundTrips(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spec := &infra.EgressProxySpec{
		Enabled:          true,
		AllowedDomains:   []string{"example.com", "*.ubuntu.com"},
		AllowedAddresses: []infra.EgressAddress{{CIDR: "203.0.113.7"}, {CIDR: "198.51.100.0/24", Protocol: "udp", Port: 123}},
	}
	obj, diags := egressProxyToModel(ctx, spec)
	if diags.HasError() {
		t.Fatal(diags)
	}
	back, diags := egressProxyToSpec(ctx, obj)
	if diags.HasError() || !reflect.DeepEqual(back, spec) {
		t.Fatalf("round trip %+v, %v", back, diags)
	}
	if obj, _ := egressProxyToModel(ctx, nil); !obj.IsNull() {
		t.Fatal("no proxy must read back as null")
	}
}
