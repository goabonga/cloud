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

func TestDiskResourceMetadataAndSchema(t *testing.T) {
	t.Parallel()

	var meta resource.MetadataResponse
	NewDiskResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "infra"}, &meta)
	if meta.TypeName != "infra_disk" {
		t.Fatalf("TypeName = %q, want infra_disk", meta.TypeName)
	}

	var sresp resource.SchemaResponse
	NewDiskResource().Schema(context.Background(), resource.SchemaRequest{}, &sresp)
	if !sresp.Schema.Attributes["size_mb"].IsRequired() {
		t.Fatal("size_mb should be required")
	}
	for _, computed := range []string{"id", "encrypted", "path", "phase"} {
		if !sresp.Schema.Attributes[computed].IsComputed() {
			t.Fatalf("%s should be computed", computed)
		}
	}
}

func TestDiskModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewDiskResource().(*genericResource[diskModel, infra.DiskSpec, infra.DiskStatus]).def
	spec := infra.DiskSpec{Name: "data", SizeMB: 1024, KMSKeyID: "key-1"}
	r := &infra.Disk{Metadata: infra.ObjectMeta{UID: "disk-1"}, Spec: spec}
	r.Status.Encrypted = true
	r.Status.Path = "/dev/vdb"
	r.Status.Phase = infra.PhaseReady

	m, diags := def.toModel(context.Background(), r)
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if !m.Encrypted.ValueBool() || m.Path.ValueString() != "/dev/vdb" || m.Phase.ValueString() != "Ready" {
		t.Fatalf("toModel() status fields = %+v", m)
	}

	got, diags := def.toSpec(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("round trip = %+v, want %+v", got, spec)
	}
	if def.id(m) != "disk-1" {
		t.Fatalf("id() = %q, want disk-1", def.id(m))
	}
}

func TestDiskFileResourceMetadataAndSchema(t *testing.T) {
	t.Parallel()

	var meta resource.MetadataResponse
	NewDiskFileResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "infra"}, &meta)
	if meta.TypeName != "infra_disk_file" {
		t.Fatalf("TypeName = %q, want infra_disk_file", meta.TypeName)
	}

	var sresp resource.SchemaResponse
	NewDiskFileResource().Schema(context.Background(), resource.SchemaRequest{}, &sresp)
	for _, required := range []string{"disk_id", "path"} {
		if !sresp.Schema.Attributes[required].IsRequired() {
			t.Fatalf("%s should be required", required)
		}
	}
	for _, computed := range []string{"id", "phase"} {
		if !sresp.Schema.Attributes[computed].IsComputed() {
			t.Fatalf("%s should be computed", computed)
		}
	}
}

func TestDiskFileModelRoundTrips(t *testing.T) {
	t.Parallel()

	def := NewDiskFileResource().(*genericResource[diskFileModel, infra.DiskFileSpec, infra.DiskFileStatus]).def
	spec := infra.DiskFileSpec{
		DiskID: "disk-1", Path: "/etc/tls/cert.pem",
		SSLCertID: "cert-1", SSLPart: infra.SSLPartCertificate,
	}
	m, diags := def.toModel(context.Background(), &infra.DiskFile{Metadata: infra.ObjectMeta{UID: "file-1"}, Spec: spec})
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
	if def.id(m) != "file-1" {
		t.Fatalf("id() = %q, want file-1", def.id(m))
	}
}
