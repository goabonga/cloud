// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestTerraformReportsUnfinishedDeletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		_, _ = w.Write([]byte(`{"metadata":{"uid":"vpc-1"}}`))
	}))
	defer server.Close()
	r := NewVPCResource()
	configureClient(t, r, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	var response resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: newTFState(t, vpcResourceSchema(t), vpcModel{ID: types.StringValue("vpc-1")})}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("unfinished deletion must retain Terraform state through an error diagnostic")
	}
}
