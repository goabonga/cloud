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

func TestSSLResourcesMetadataAndSchema(t *testing.T) {
	t.Parallel()

	cases := []struct {
		factory  func() resource.Resource
		wantType string
		required []string
	}{
		{NewSSLCAResource, "infra_ssl_ca", []string{"common_name"}},
		{NewSSLCertResource, "infra_ssl_cert", []string{"ca_id", "common_name"}},
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
			for _, computed := range []string{"id", "cert_pem", "phase"} {
				if !sresp.Schema.Attributes[computed].IsComputed() {
					t.Fatalf("%s should be computed", computed)
				}
			}
		})
	}
}

func TestSSLCAModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewSSLCAResource().(*genericResource[sslCAModel, infra.SSLCASpec, infra.SSLCAStatus]).def
	spec := infra.SSLCASpec{
		CommonName: "Internal CA", Organization: "ACME", ValidDays: 3650,
		VPCIDs: []string{"vpc-1", "vpc-2"},
	}
	r := &infra.SSLCA{Metadata: infra.ObjectMeta{UID: "ca-1"}, Spec: spec}
	r.Status.CertPEM = []byte("-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----\n")
	r.Status.Phase = infra.PhaseReady

	m, diags := def.toModel(context.Background(), r)
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if m.CertPEM.ValueString() != string(r.Status.CertPEM) {
		t.Fatalf("cert_pem = %q, want %q", m.CertPEM.ValueString(), r.Status.CertPEM)
	}

	got, diags := def.toSpec(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("round trip = %+v, want %+v", got, spec)
	}
	if def.id(m) != "ca-1" {
		t.Fatalf("id() = %q, want ca-1", def.id(m))
	}
}

func TestSSLCertModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewSSLCertResource().(*genericResource[sslCertModel, infra.SSLCertSpec, infra.SSLCertStatus]).def
	spec := infra.SSLCertSpec{
		CAID: "ca-1", CommonName: "web.internal",
		DNSNames:    []string{"web.internal", "www.web.internal"},
		IPAddresses: []string{"10.0.0.5"},
		ValidDays:   365,
	}
	r := &infra.SSLCert{Metadata: infra.ObjectMeta{UID: "cert-1"}, Spec: spec}
	r.Status.CertPEM = []byte("-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----\n")
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
	if def.id(m) != "cert-1" {
		t.Fatalf("id() = %q, want cert-1", def.id(m))
	}
}
