// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package provider_test

import (
	"context"
	"testing"

	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/goabonga/infrastructure/internal/provider"
	"github.com/goabonga/infrastructure/internal/resources"
)

// newProviderConfig builds a tfsdk.Config conforming to the provider schema,
// with endpoint/token left null when the pointer is nil - the same shape
// Terraform sends for an unset optional attribute.
func newProviderConfig(t *testing.T, schemaResp fwprovider.SchemaResponse, endpoint, token *string, projects ...*string) tfsdk.Config {
	t.Helper()
	ctx := context.Background()
	objType := schemaResp.Schema.Type().TerraformType(ctx)

	toValue := func(s *string) tftypes.Value {
		if s == nil {
			return tftypes.NewValue(tftypes.String, nil)
		}
		return tftypes.NewValue(tftypes.String, *s)
	}

	var project *string
	if len(projects) > 0 {
		project = projects[0]
	}
	raw := tftypes.NewValue(objType, map[string]tftypes.Value{
		"endpoint":   toValue(endpoint),
		"token":      toValue(token),
		"project_id": toValue(project),
	})
	return tfsdk.Config{Raw: raw, Schema: schemaResp.Schema}
}

func providerSchema(t *testing.T, p fwprovider.Provider) fwprovider.SchemaResponse {
	t.Helper()
	var resp fwprovider.SchemaResponse
	p.Schema(context.Background(), fwprovider.SchemaRequest{}, &resp)
	return resp
}

func TestProviderConfigureDefaults(t *testing.T) {
	t.Parallel()

	p := provider.New("")()
	schemaResp := providerSchema(t, p)

	req := fwprovider.ConfigureRequest{Config: newProviderConfig(t, schemaResp, nil, nil)}
	var resp fwprovider.ConfigureResponse
	p.Configure(context.Background(), req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure diagnostics: %v", resp.Diagnostics)
	}
	cfg, ok := resp.ResourceData.(resources.ProviderConfig)
	if !ok {
		t.Fatalf("ResourceData = %T, want resources.ProviderConfig", resp.ResourceData)
	}
	if cfg.Endpoint != "http://localhost:8080" {
		t.Fatalf("Endpoint = %q, want the default", cfg.Endpoint)
	}
	if cfg.Token != "" {
		t.Fatalf("Token = %q, want empty with no env var and no config", cfg.Token)
	}
}

func TestProviderConfigureExplicitValues(t *testing.T) {
	t.Parallel()

	p := provider.New("")()
	schemaResp := providerSchema(t, p)

	endpoint, token := "https://infra.example.com", "cfg-token"
	req := fwprovider.ConfigureRequest{Config: newProviderConfig(t, schemaResp, &endpoint, &token)}
	var resp fwprovider.ConfigureResponse
	p.Configure(context.Background(), req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure diagnostics: %v", resp.Diagnostics)
	}
	cfg := resp.ResourceData.(resources.ProviderConfig)
	if cfg.Endpoint != endpoint {
		t.Fatalf("Endpoint = %q, want %q", cfg.Endpoint, endpoint)
	}
	if cfg.Token != token {
		t.Fatalf("Token = %q, want %q", cfg.Token, token)
	}
}

func TestProviderConfigureTokenFallsBackToEnv(t *testing.T) {
	t.Setenv("GOA_API_TOKEN", "env-token")

	p := provider.New("")()
	schemaResp := providerSchema(t, p)

	req := fwprovider.ConfigureRequest{Config: newProviderConfig(t, schemaResp, nil, nil)}
	var resp fwprovider.ConfigureResponse
	p.Configure(context.Background(), req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure diagnostics: %v", resp.Diagnostics)
	}
	cfg := resp.ResourceData.(resources.ProviderConfig)
	if cfg.Token != "env-token" {
		t.Fatalf("Token = %q, want the GOA_API_TOKEN fallback", cfg.Token)
	}
}

func TestProviderConfigureReturnsConfigDiagnostics(t *testing.T) {
	t.Parallel()

	p := provider.New("")()
	schemaResp := providerSchema(t, p)

	// Raw's own type (endpoint: bool) disagrees with the schema's (endpoint:
	// string): req.Config.Get must report that mismatch as a diagnostic
	// rather than populate cfg, so Configure returns before setting
	// ResourceData.
	mismatchedType := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"endpoint": tftypes.Bool,
		"token":    tftypes.String,
	}}
	badRaw := tftypes.NewValue(mismatchedType, map[string]tftypes.Value{
		"endpoint": tftypes.NewValue(tftypes.Bool, true),
		"token":    tftypes.NewValue(tftypes.String, nil),
	})
	req := fwprovider.ConfigureRequest{Config: tfsdk.Config{Raw: badRaw, Schema: schemaResp.Schema}}
	var resp fwprovider.ConfigureResponse
	p.Configure(context.Background(), req, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Configure should report a diagnostic error for a malformed config")
	}
	if resp.ResourceData != nil {
		t.Fatalf("ResourceData = %v, want nil after a config error", resp.ResourceData)
	}
}

func TestProviderConfigureEmptyConfigValuesDoNotOverrideDefaults(t *testing.T) {
	t.Parallel()

	empty := ""
	p := provider.New("")()
	schemaResp := providerSchema(t, p)

	req := fwprovider.ConfigureRequest{Config: newProviderConfig(t, schemaResp, &empty, &empty)}
	var resp fwprovider.ConfigureResponse
	p.Configure(context.Background(), req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure diagnostics: %v", resp.Diagnostics)
	}
	cfg := resp.ResourceData.(resources.ProviderConfig)
	if cfg.Endpoint != "http://localhost:8080" {
		t.Fatalf("Endpoint = %q, want the default when config sets an empty string", cfg.Endpoint)
	}
	if cfg.Token != "" {
		t.Fatalf("Token = %q, want empty", cfg.Token)
	}
}

func TestProviderProjectConfiguration(t *testing.T) {
	t.Setenv("GOA_PROJECT_ID", "env-project")
	for _, tc := range []struct {
		name       string
		configured *string
		want       string
	}{
		{name: "environment", want: "env-project"},
		{name: "explicit", configured: ptr("explicit-project"), want: "explicit-project"},
		{name: "explicit empty", configured: ptr(""), want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := provider.New("")()
			var resp fwprovider.ConfigureResponse
			p.Configure(context.Background(), fwprovider.ConfigureRequest{Config: newProviderConfig(t, providerSchema(t, p), nil, nil, tc.configured)}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if got := resp.ResourceData.(resources.ProviderConfig).ProjectID; got != tc.want {
				t.Fatalf("project: %q, want %q", got, tc.want)
			}
		})
	}
}
func ptr(s string) *string { return &s }
