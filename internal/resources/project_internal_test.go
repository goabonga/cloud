// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package resources

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestTerraformPreservesProjectMetadata(t *testing.T) {
	ctx := context.Background()
	var received infra.ObjectMeta
	existing := infra.ObjectMeta{UID: "vpc-1", ProjectID: "project-1", OwnerUID: "alice", Generation: 3, ResourceVersion: "revision-3"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		envelope := infra.VPC{Metadata: existing, Spec: infra.VPCSpec{CIDR: "10.0.0.0/16"}}
		if req.Method == http.MethodPut {
			if err := json.NewDecoder(req.Body).Decode(&envelope); err != nil {
				t.Error(err)
				return
			}
			received = envelope.Metadata
		}
		_ = json.NewEncoder(w).Encode(envelope)
	}))
	defer server.Close()
	r := NewVPCResource()
	var configured resource.ConfigureResponse
	r.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: ProviderConfig{Endpoint: server.URL, ProjectID: "project-1"}}, &configured)
	sch := vpcResourceSchema(t)
	model := vpcModel{ID: types.StringValue("vpc-1"), CIDR: types.StringValue("10.1.0.0/16")}
	response := resource.UpdateResponse{State: tfsdk.State{Schema: sch}}
	r.Update(ctx, resource.UpdateRequest{Plan: newTFPlan(t, sch, model)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	if received.ProjectID != existing.ProjectID || received.OwnerUID != existing.OwnerUID || received.Generation != existing.Generation || received.ResourceVersion != existing.ResourceVersion {
		t.Fatalf("metadata discarded: %#v", received)
	}
	responseCreate := resource.CreateResponse{State: tfsdk.State{Schema: sch}}
	r.Create(ctx, resource.CreateRequest{Plan: newTFPlan(t, sch, model)}, &responseCreate)
	if responseCreate.Diagnostics.HasError() {
		t.Fatal(responseCreate.Diagnostics)
	}
	if received.ProjectID != "project-1" {
		t.Fatal("provider project omitted on create")
	}
}

func TestTerraformRejectsProjectMove(t *testing.T) {
	ctx := context.Background()
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPut {
			puts++
		}
		_ = json.NewEncoder(w).Encode(infra.VPC{Metadata: infra.ObjectMeta{UID: "vpc-1", ProjectID: "original"}})
	}))
	defer server.Close()
	r := NewVPCResource()
	var configured resource.ConfigureResponse
	r.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: ProviderConfig{Endpoint: server.URL, ProjectID: "other"}}, &configured)
	sch := vpcResourceSchema(t)
	model := vpcModel{ID: types.StringValue("vpc-1"), CIDR: types.StringValue("10.1.0.0/16")}
	response := resource.UpdateResponse{State: tfsdk.State{Schema: sch}}
	r.Update(ctx, resource.UpdateRequest{Plan: newTFPlan(t, sch, model)}, &response)
	if !response.Diagnostics.HasError() || puts != 0 {
		t.Fatalf("project move was submitted: %v, puts=%d", response.Diagnostics, puts)
	}
}
