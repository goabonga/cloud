// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"
	"reflect"
	"testing"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestDNSZoneModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewDNSZoneResource().(*genericResource[dnsZoneModel, infra.DNSZoneSpec, infra.DNSZoneStatus]).def
	spec := infra.DNSZoneSpec{
		Name: "internal", Domain: "internal.example.com", Visibility: "private",
		VPCIDs: []string{"vpc-1", "vpc-2"},
	}
	m, diags := def.toModel(context.Background(), &infra.DNSZone{Metadata: infra.ObjectMeta{UID: "zone-1"}, Spec: spec})
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
	if def.id(m) != "zone-1" {
		t.Fatalf("id() = %q, want zone-1", def.id(m))
	}
}

func TestDNSRecordModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewDNSRecordResource().(*genericResource[dnsRecordModel, infra.DNSRecordSpec, infra.DNSRecordStatus]).def
	spec := infra.DNSRecordSpec{
		ZoneID: "zone-1", Name: "www", Type: "A", TTL: 300,
		Records: []string{"10.0.0.1", "10.0.0.2"},
	}
	m, diags := def.toModel(context.Background(), &infra.DNSRecord{Metadata: infra.ObjectMeta{UID: "record-1"}, Spec: spec})
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
	if def.id(m) != "record-1" {
		t.Fatalf("id() = %q, want record-1", def.id(m))
	}
}
