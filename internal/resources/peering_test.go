// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"
	"reflect"
	"testing"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestPeeringModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewPeeringResource().(*genericResource[peeringModel, infra.PeeringSpec, infra.PeeringStatus]).def
	spec := infra.PeeringSpec{VPC1ID: "vpc-1", VPC2ID: "vpc-2"}
	r := &infra.Peering{Metadata: infra.ObjectMeta{UID: "peering-1"}, Spec: spec}
	r.Status.Veth1 = "veth-a"
	r.Status.Veth2 = "veth-b"
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
	if def.id(m) != "peering-1" {
		t.Fatalf("id() = %q, want peering-1", def.id(m))
	}
}
