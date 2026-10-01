// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"
	"reflect"
	"testing"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestLoadBalancerModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewLoadBalancerResource().(*genericResource[loadBalancerModel, infra.LoadBalancerSpec, infra.LoadBalancerStatus]).def
	spec := infra.LoadBalancerSpec{
		Name: "web", VPCID: "vpc-1", Address: "10.0.0.5", Port: 443,
		Protocol: "tcp", Algorithm: "round_robin", PublicIPID: "ip-1",
	}
	r := &infra.LoadBalancer{Metadata: infra.ObjectMeta{UID: "lb-1"}, Spec: spec}
	r.Status.ServiceID = "svc-1"
	r.Status.PublicAddress = "203.0.113.9"
	r.Status.Phase = infra.PhaseReady

	m, diags := def.toModel(context.Background(), r)
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if m.ServiceID.ValueString() != "svc-1" || m.PublicAddress.ValueString() != "203.0.113.9" {
		t.Fatalf("toModel() status fields = %+v", m)
	}
	// Without a status address, the spec's own address is used.
	if m.Address.ValueString() != spec.Address {
		t.Fatalf("address = %q, want the spec's %q", m.Address.ValueString(), spec.Address)
	}

	got, diags := def.toSpec(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("round trip = %+v, want %+v", got, spec)
	}
	if def.id(m) != "lb-1" {
		t.Fatalf("id() = %q, want lb-1", def.id(m))
	}
}

func TestLoadBalancerModelPrefersTheStatusAddress(t *testing.T) {
	t.Parallel()

	def := NewLoadBalancerResource().(*genericResource[loadBalancerModel, infra.LoadBalancerSpec, infra.LoadBalancerStatus]).def
	spec := infra.LoadBalancerSpec{VPCID: "vpc-1", Address: "10.0.0.5"}
	r := &infra.LoadBalancer{Metadata: infra.ObjectMeta{UID: "lb-1"}, Spec: spec}
	r.Status.Address = "10.0.0.99"

	m, diags := def.toModel(context.Background(), r)
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if m.Address.ValueString() != "10.0.0.99" {
		t.Fatalf("address = %q, want the resolved status address", m.Address.ValueString())
	}
}

func TestLBBackendModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewLBBackendResource().(*genericResource[lbBackendModel, infra.LBBackendSpec, infra.LBBackendStatus]).def
	spec := infra.LBBackendSpec{LBID: "lb-1", ComputeID: "compute-1", Port: 8080, Weight: 10}
	r := &infra.LBBackend{Metadata: infra.ObjectMeta{UID: "backend-1"}, Spec: spec}
	r.Status.RealServerIP = "10.0.0.20"
	r.Status.Phase = infra.PhaseReady

	m, diags := def.toModel(context.Background(), r)
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if m.RealServerIP.ValueString() != "10.0.0.20" {
		t.Fatalf("real_server_ip = %q, want 10.0.0.20", m.RealServerIP.ValueString())
	}

	got, diags := def.toSpec(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("round trip = %+v, want %+v", got, spec)
	}
	if def.id(m) != "backend-1" {
		t.Fatalf("id() = %q, want backend-1", def.id(m))
	}
}
