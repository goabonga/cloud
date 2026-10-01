// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"
	"reflect"
	"testing"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestNodeModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewNodeResource().(*genericResource[nodeModel, infra.NodeSpec, infra.NodeStatus]).def
	spec := infra.NodeSpec{
		Hostname: "node-1", Address: "10.0.0.10",
		Labels:   map[string]string{"zone": "a"},
		Capacity: infra.NodeCapacity{CPUs: 8, MemoryMB: 16384, MaxPods: 110},
	}
	r := &infra.Node{Metadata: infra.ObjectMeta{UID: "node-1"}, Spec: spec}
	r.Status.LastSeen = "2026-10-01T12:00:00Z"
	r.Status.Phase = infra.PhaseReady

	m, diags := def.toModel(context.Background(), r)
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if m.LastSeen.ValueString() != "2026-10-01T12:00:00Z" {
		t.Fatalf("last_seen = %q", m.LastSeen.ValueString())
	}

	got, diags := def.toSpec(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("round trip = %+v, want %+v", got, spec)
	}
	if def.id(m) != "node-1" {
		t.Fatalf("id() = %q, want node-1", def.id(m))
	}
}

func TestNodePoolModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewNodePoolResource().(*genericResource[nodePoolModel, infra.NodePoolSpec, infra.NodePoolStatus]).def
	spec := infra.NodePoolSpec{
		Name:         "default",
		NodeSelector: map[string]string{"tier": "general"},
		MinNodes:     1,
		MaxNodes:     5,
	}
	r := &infra.NodePool{Metadata: infra.ObjectMeta{UID: "pool-1"}, Spec: spec}
	r.Status.ReadyNodes = 3
	r.Status.TotalNodes = 4
	r.Status.Phase = infra.PhaseReady

	m, diags := def.toModel(context.Background(), r)
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if m.ReadyNodes.ValueInt64() != 3 || m.TotalNodes.ValueInt64() != 4 {
		t.Fatalf("toModel() status fields = %+v", m)
	}

	got, diags := def.toSpec(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("round trip = %+v, want %+v", got, spec)
	}
	if def.id(m) != "pool-1" {
		t.Fatalf("id() = %q, want pool-1", def.id(m))
	}
}
