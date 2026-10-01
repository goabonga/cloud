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

func TestKMSResourcesMetadataAndSchema(t *testing.T) {
	t.Parallel()

	cases := []struct {
		factory  func() resource.Resource
		wantType string
		required []string
	}{
		{NewKMSKeyringResource, "infra_kms_keyring", []string{"name"}},
		{NewKMSKeyResource, "infra_kms_key", []string{"keyring_id", "name"}},
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

func TestKMSKeyringModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewKMSKeyringResource().(*genericResource[kmsKeyringModel, infra.KMSKeyringSpec, infra.KMSKeyringStatus]).def
	spec := infra.KMSKeyringSpec{Name: "prod"}
	r := &infra.KMSKeyring{Metadata: infra.ObjectMeta{UID: "keyring-1"}, Spec: spec}
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
	if def.id(m) != "keyring-1" {
		t.Fatalf("id() = %q, want keyring-1", def.id(m))
	}
}

func TestKMSKeyModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewKMSKeyResource().(*genericResource[kmsKeyModel, infra.KMSKeySpec, infra.KMSKeyStatus]).def
	spec := infra.KMSKeySpec{
		KeyringID: "keyring-1", Name: "db-key",
		Purpose: "encrypt", Algorithm: "AES-256", RotationPeriod: "90d",
	}
	r := &infra.KMSKey{Metadata: infra.ObjectMeta{UID: "key-1"}, Spec: spec}
	r.Status.ActiveVersion = 2
	r.Status.Phase = infra.PhaseReady

	m, diags := def.toModel(context.Background(), r)
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if m.ActiveVersion.ValueInt64() != 2 {
		t.Fatalf("active_version = %d, want 2", m.ActiveVersion.ValueInt64())
	}

	got, diags := def.toSpec(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("round trip = %+v, want %+v", got, spec)
	}
	if def.id(m) != "key-1" {
		t.Fatalf("id() = %q, want key-1", def.id(m))
	}
}
