// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package resources implements the Terraform resources exposed by the provider.
// A generic CRUD core maps a Terraform model onto the API client, so each
// resource only declares its model, schema and the spec/model conversions.
package resources

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"

	"github.com/goabonga/infrastructure/internal/client"
	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

// resourceDef describes how to map a Terraform model M onto an API resource of
// spec S and status ST for one kind.
type resourceDef[M any, S any, ST any] struct {
	kind   string
	schema schema.Schema
	// toSpec extracts the API spec from a planned model.
	toSpec func(ctx context.Context, m M) (S, diag.Diagnostics)
	// toModel builds the state model from an API resource (id + computed fields).
	toModel func(ctx context.Context, r *infra.Resource[S, ST]) (M, diag.Diagnostics)
	// id returns the model's id.
	id func(M) string
}

type genericResource[M any, S any, ST any] struct {
	def       resourceDef[M, S, ST]
	client    *client.Client[S, ST]
	projectID string
}

// newGeneric builds a Terraform resource from a definition.
func newGeneric[M any, S any, ST any](def resourceDef[M, S, ST]) resource.Resource {
	return &genericResource[M, S, ST]{def: def}
}

func (r *genericResource[M, S, ST]) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.def.kind
}

func (r *genericResource[M, S, ST]) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = r.def.schema
}

func (r *genericResource[M, S, ST]) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	cfg, ok := req.ProviderData.(ProviderConfig)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected ProviderConfig, got %T", req.ProviderData))
		return
	}
	var opts []client.Option
	if cfg.Token != "" {
		opts = append(opts, client.WithToken(cfg.Token))
	}
	r.client = client.New[S, ST](cfg.Endpoint, r.def.kind, opts...)
	r.projectID = cfg.ProjectID
}

func (r *genericResource[M, S, ST]) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan M
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	spec, d := r.def.toSpec(ctx, plan)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := newID(r.def.kind)
	out, err := r.client.Put(ctx, &infra.Resource[S, ST]{Metadata: infra.ObjectMeta{UID: id, ProjectID: r.projectID}, Spec: spec})
	if err != nil {
		resp.Diagnostics.AddError("Create "+r.def.kind, err.Error())
		return
	}
	r.setState(ctx, out, &resp.Diagnostics, &resp.State)
}

func (r *genericResource[M, S, ST]) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state M
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client.Get(ctx, r.def.id(state))
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Read "+r.def.kind, err.Error())
		return
	}
	r.setState(ctx, out, &resp.Diagnostics, &resp.State)
}

func (r *genericResource[M, S, ST]) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan M
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	spec, d := r.def.toSpec(ctx, plan)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	existing, err := r.client.Get(ctx, r.def.id(plan))
	if err != nil {
		resp.Diagnostics.AddError("Read before update "+r.def.kind, err.Error())
		return
	}
	if r.projectID != "" && existing.Metadata.ProjectID != r.projectID {
		resp.Diagnostics.AddError("Project mismatch", "The existing resource belongs to a different project; moving resources is not supported.")
		return
	}
	out, err := r.client.Put(ctx, &infra.Resource[S, ST]{Metadata: existing.Metadata, Spec: spec})
	if err != nil {
		resp.Diagnostics.AddError("Update "+r.def.kind, err.Error())
		return
	}
	r.setState(ctx, out, &resp.Diagnostics, &resp.State)
}

func (r *genericResource[M, S, ST]) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state M
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Delete(ctx, r.def.id(state)); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Delete "+r.def.kind, err.Error())
	}
}

func (r *genericResource[M, S, ST]) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// stateSetter is implemented by both *resource.CreateResponse.State-style states.
type stateSetter interface {
	Set(ctx context.Context, val any) diag.Diagnostics
}

func (r *genericResource[M, S, ST]) setState(ctx context.Context, out *infra.Resource[S, ST], diags *diag.Diagnostics, state stateSetter) {
	model, d := r.def.toModel(ctx, out)
	diags.Append(d...)
	if diags.HasError() {
		return
	}
	diags.Append(state.Set(ctx, model)...)
}

// newID returns a fresh "<kind>-<hex>" identifier.
func newID(kind string) string {
	b := make([]byte, 6)
	// rand.Read never returns a non-nil error: it crashes the process
	// instead (see crypto/rand.Read's doc, https://go.dev/issue/66821).
	_, _ = rand.Read(b)
	return kind + "-" + hex.EncodeToString(b)
}

// idAttribute is the standard computed id attribute.
func idAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		Computed:            true,
		MarkdownDescription: "System-assigned unique identifier.",
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

// phaseAttribute is the standard computed lifecycle-phase attribute.
func phaseAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		Computed:            true,
		MarkdownDescription: "Lifecycle phase reported by the control plane.",
	}
}
