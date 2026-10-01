// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package provider_test

import (
	"context"
	"testing"

	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"

	"github.com/goabonga/infrastructure/internal/provider"
)

func TestProviderMetadata(t *testing.T) {
	t.Parallel()

	p := provider.New("1.2.3")()
	var resp fwprovider.MetadataResponse
	p.Metadata(context.Background(), fwprovider.MetadataRequest{}, &resp)

	if resp.TypeName != "infra" {
		t.Fatalf("TypeName = %q, want infra", resp.TypeName)
	}
	if resp.Version != "1.2.3" {
		t.Fatalf("Version = %q, want 1.2.3", resp.Version)
	}
}

func TestProviderSchemaHasEndpoint(t *testing.T) {
	t.Parallel()

	p := provider.New("")()
	var resp fwprovider.SchemaResponse
	p.Schema(context.Background(), fwprovider.SchemaRequest{}, &resp)

	if _, ok := resp.Schema.Attributes["endpoint"]; !ok {
		t.Fatalf("schema missing endpoint attribute: %+v", resp.Schema.Attributes)
	}
	tok, ok := resp.Schema.Attributes["token"]
	if !ok {
		t.Fatalf("schema missing token attribute: %+v", resp.Schema.Attributes)
	}
	if !tok.IsSensitive() {
		t.Fatal("token attribute should be sensitive")
	}
}

func TestProviderRegistersResources(t *testing.T) {
	t.Parallel()

	p := provider.New("")()
	if got := len(p.Resources(context.Background())); got != 26 {
		t.Fatalf("Resources() len = %d, want 26", got)
	}
	if ds := p.DataSources(context.Background()); ds != nil {
		t.Fatalf("DataSources() = %v, want nil", ds)
	}
}
