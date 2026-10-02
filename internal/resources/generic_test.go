// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// This file exercises genericResource's Configure/Create/Read/Update/Delete/
// ImportState: the CRUD core shared by every resource in this package (each
// one only supplies a resourceDef). Covering it once through infra_vpc, the
// simplest resourceDef, covers it for all of them.

func vpcResourceSchema(t *testing.T) schema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	NewVPCResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	return resp.Schema
}

func newTFPlan(t *testing.T, sch schema.Schema, model any) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: sch}
	if diags := plan.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("Plan.Set: %v", diags)
	}
	return plan
}

func newTFState(t *testing.T, sch schema.Schema, model any) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: sch}
	if diags := state.Set(context.Background(), model); diags.HasError() {
		t.Fatalf("State.Set: %v", diags)
	}
	return state
}

func configureClient(t *testing.T, r resource.Resource, endpoint string) {
	t.Helper()
	cr, ok := r.(resource.ResourceWithConfigure)
	if !ok {
		t.Fatalf("%T does not implement ResourceWithConfigure", r)
	}
	var resp resource.ConfigureResponse
	cr.Configure(context.Background(), resource.ConfigureRequest{ProviderData: ProviderConfig{Endpoint: endpoint, Token: "tok-123"}}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure: %v", resp.Diagnostics)
	}
}

// fakeVPCAPI serves a minimal stand-in for the control-plane API. PUT echoes
// the request envelope back with status merged in, so the generic Create and
// Update paths see a realistic round trip; GET and DELETE treat the uid
// "missing" as not-found, the way the real API answers a 404.
func fakeVPCAPI(t *testing.T, status map[string]any) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if uid == "missing" && r.Method != http.MethodPut {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodPut:
			var doc map[string]any
			if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if status != nil {
				doc["status"] = status
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doc)
		case http.MethodGet:
			doc := map[string]any{
				"apiVersion": "infra/v1",
				"kind":       "vpc",
				"metadata":   map[string]any{"uid": uid},
				"spec":       map[string]any{"cidr": "10.0.0.0/16"},
				"status":     status,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doc)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

// alwaysErrorAPI answers every request with a server error, for testing how
// the generic CRUD core surfaces a backend failure.
func alwaysErrorAPI(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestGenericResourceConfigure(t *testing.T) {
	r := NewVPCResource().(resource.ResourceWithConfigure)
	ctx := context.Background()

	// Terraform calls Configure with nil ProviderData before the provider has
	// been configured (e.g. during validation); it must be a no-op.
	var nilResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{}, &nilResp)
	if nilResp.Diagnostics.HasError() {
		t.Fatalf("Configure(nil ProviderData): %v", nilResp.Diagnostics)
	}

	// Any other type is a provider bug and must error clearly.
	var wrongResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "not-a-provider-config"}, &wrongResp)
	if !wrongResp.Diagnostics.HasError() {
		t.Fatal("Configure with the wrong provider data type must error")
	}

	// A config without a token still configures the client.
	var noTokenResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: ProviderConfig{Endpoint: "http://127.0.0.1:0"}}, &noTokenResp)
	if noTokenResp.Diagnostics.HasError() {
		t.Fatalf("Configure without a token: %v", noTokenResp.Diagnostics)
	}
}

func TestGenericResourceCRUDLifecycle(t *testing.T) {
	ctx := context.Background()
	sch := vpcResourceSchema(t)

	ts := fakeVPCAPI(t, map[string]any{"phase": "Ready", "bridgeName": "br-vpc0"})
	r := NewVPCResource()
	configureClient(t, r, ts.URL)

	// Create.
	plan := newTFPlan(t, sch, vpcModel{CIDR: types.StringValue("10.0.0.0/16")})
	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: sch}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", createResp.Diagnostics)
	}
	var created vpcModel
	if diags := createResp.State.Get(ctx, &created); diags.HasError() {
		t.Fatalf("decode created state: %v", diags)
	}
	if !strings.HasPrefix(created.ID.ValueString(), "vpc-") {
		t.Fatalf("created id = %q, want a vpc- prefix", created.ID.ValueString())
	}
	if created.BridgeName.ValueString() != "br-vpc0" || created.Phase.ValueString() != "Ready" {
		t.Fatalf("created status fields = %+v", created)
	}

	// Read, found.
	var readResp resource.ReadResponse
	readResp.State = tfsdk.State{Schema: sch}
	r.Read(ctx, resource.ReadRequest{State: newTFState(t, sch, created)}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", readResp.Diagnostics)
	}
	var readBack vpcModel
	if diags := readResp.State.Get(ctx, &readBack); diags.HasError() {
		t.Fatalf("decode read state: %v", diags)
	}
	if readBack.ID.ValueString() != created.ID.ValueString() || readBack.CIDR.ValueString() != created.CIDR.ValueString() {
		t.Fatalf("Read did not round-trip: %+v", readBack)
	}

	// Read, not found: the resource must be dropped from state, not errored.
	missing := vpcModel{ID: types.StringValue("missing"), CIDR: types.StringValue("10.0.0.0/16")}
	var notFoundResp resource.ReadResponse
	notFoundResp.State = tfsdk.State{Schema: sch}
	r.Read(ctx, resource.ReadRequest{State: newTFState(t, sch, missing)}, &notFoundResp)
	if notFoundResp.Diagnostics.HasError() {
		t.Fatalf("Read (not found): %v", notFoundResp.Diagnostics)
	}
	if !notFoundResp.State.Raw.IsNull() {
		t.Fatal("a 404 on Read must remove the resource from state")
	}

	// Update.
	updated := created
	updated.CIDR = types.StringValue("10.1.0.0/16")
	var updateResp resource.UpdateResponse
	updateResp.State = tfsdk.State{Schema: sch}
	r.Update(ctx, resource.UpdateRequest{Plan: newTFPlan(t, sch, updated)}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", updateResp.Diagnostics)
	}
	var afterUpdate vpcModel
	if diags := updateResp.State.Get(ctx, &afterUpdate); diags.HasError() {
		t.Fatalf("decode updated state: %v", diags)
	}
	if afterUpdate.CIDR.ValueString() != "10.1.0.0/16" {
		t.Fatalf("updated cidr = %q, want 10.1.0.0/16", afterUpdate.CIDR.ValueString())
	}

	// Delete, found.
	var deleteResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: newTFState(t, sch, created)}, &deleteResp)
	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("Delete: %v", deleteResp.Diagnostics)
	}

	// Delete, not found: a no-op, not an error (the end state is the same).
	var deleteMissingResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: newTFState(t, sch, missing)}, &deleteMissingResp)
	if deleteMissingResp.Diagnostics.HasError() {
		t.Fatalf("Delete (not found): %v", deleteMissingResp.Diagnostics)
	}

	// ImportState passes the given id through to the id attribute.
	importer := r.(resource.ResourceWithImportState)
	var importResp resource.ImportStateResponse
	importResp.State = tfsdk.State{Schema: sch, Raw: tftypes.NewValue(sch.Type().TerraformType(ctx), nil)}
	importer.ImportState(ctx, resource.ImportStateRequest{ID: "vpc-imported"}, &importResp)
	if importResp.Diagnostics.HasError() {
		t.Fatalf("ImportState: %v", importResp.Diagnostics)
	}
	var imported vpcModel
	if diags := importResp.State.Get(ctx, &imported); diags.HasError() {
		t.Fatalf("decode imported state: %v", diags)
	}
	if imported.ID.ValueString() != "vpc-imported" {
		t.Fatalf("imported id = %q, want vpc-imported", imported.ID.ValueString())
	}
}

func TestGenericResourceBackendErrors(t *testing.T) {
	ctx := context.Background()
	sch := vpcResourceSchema(t)
	ts := alwaysErrorAPI(t)
	r := NewVPCResource()
	configureClient(t, r, ts.URL)

	model := vpcModel{ID: types.StringValue("vpc-1"), CIDR: types.StringValue("10.0.0.0/16")}

	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: sch}
	r.Create(ctx, resource.CreateRequest{Plan: newTFPlan(t, sch, model)}, &createResp)
	if !createResp.Diagnostics.HasError() {
		t.Fatal("Create must surface a backend error")
	}

	var readResp resource.ReadResponse
	readResp.State = tfsdk.State{Schema: sch}
	r.Read(ctx, resource.ReadRequest{State: newTFState(t, sch, model)}, &readResp)
	if !readResp.Diagnostics.HasError() {
		t.Fatal("Read must surface a backend error that isn't a 404")
	}

	var updateResp resource.UpdateResponse
	updateResp.State = tfsdk.State{Schema: sch}
	r.Update(ctx, resource.UpdateRequest{Plan: newTFPlan(t, sch, model)}, &updateResp)
	if !updateResp.Diagnostics.HasError() {
		t.Fatal("Update must surface a backend error")
	}

	var deleteResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: newTFState(t, sch, model)}, &deleteResp)
	if !deleteResp.Diagnostics.HasError() {
		t.Fatal("Delete must surface a backend error that isn't a 404")
	}
}

// mismatchedVPCSchema declares "cidr" as a number instead of a string, so
// decoding a plan or state built from the real infra_vpc schema into it fails
// with a diagnostic (a Value Conversion Error), the same way a provider bug
// would surface one, rather than panicking.
func mismatchedVPCSchema() schema.Schema {
	return schema.Schema{Attributes: map[string]schema.Attribute{
		"id":          idAttribute(),
		"cidr":        schema.Int64Attribute{Required: true},
		"bridge_name": schema.StringAttribute{Computed: true},
		"phase":       schema.StringAttribute{Computed: true},
	}}
}

func TestGenericResourceReportsAMalformedPlanOrStateAsADiagnostic(t *testing.T) {
	ctx := context.Background()
	sch := vpcResourceSchema(t)
	bad := mismatchedVPCSchema()
	r := NewVPCResource()
	model := vpcModel{ID: types.StringValue("vpc-1"), CIDR: types.StringValue("10.0.0.0/16")}

	// Create: Plan.Get fails before the client is ever consulted.
	plan := newTFPlan(t, sch, model)
	plan.Schema = bad
	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: sch}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &createResp)
	if !createResp.Diagnostics.HasError() {
		t.Fatal("Create must report a malformed plan as a diagnostic, not panic")
	}

	// Read: State.Get fails the same way.
	state := newTFState(t, sch, model)
	state.Schema = bad
	var readResp resource.ReadResponse
	readResp.State = tfsdk.State{Schema: sch}
	r.Read(ctx, resource.ReadRequest{State: state}, &readResp)
	if !readResp.Diagnostics.HasError() {
		t.Fatal("Read must report a malformed state as a diagnostic, not panic")
	}

	// Update: Plan.Get fails the same way.
	plan2 := newTFPlan(t, sch, model)
	plan2.Schema = bad
	var updateResp resource.UpdateResponse
	updateResp.State = tfsdk.State{Schema: sch}
	r.Update(ctx, resource.UpdateRequest{Plan: plan2}, &updateResp)
	if !updateResp.Diagnostics.HasError() {
		t.Fatal("Update must report a malformed plan as a diagnostic, not panic")
	}

	// Delete: State.Get fails the same way.
	state2 := newTFState(t, sch, model)
	state2.Schema = bad
	var deleteResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state2}, &deleteResp)
	if !deleteResp.Diagnostics.HasError() {
		t.Fatal("Delete must report a malformed state as a diagnostic, not panic")
	}
}

// newID's error branch (crypto/rand.Read failing) is not exercised here: since
// Go 1.24, crypto/rand.Read calls runtime fatal() instead of returning an
// error when its entropy source fails (https://go.dev/issue/66821), so that
// branch is unreachable from a test without crashing the process. newID's
// success path is already covered by TestNewID in vpc_test.go and by
// TestGenericResourceCRUDLifecycle's Create call above.
//
// Likewise, the toSpec-returned-an-error branches in Create/Update and the
// toModel-returned-an-error branch in setState are not exercised: every
// resourceDef's toSpec/toModel in this package is a straightforward field
// conversion over already-well-typed data (decoded against the same schema
// that produced it) and never itself returns an error diagnostic, so those
// branches are not reachable without making a conversion function misbehave
// only for the test, which would test the test rather than the code.
