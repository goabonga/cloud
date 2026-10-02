// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

type peeringModel struct {
	ID     types.String `tfsdk:"id"`
	VPC1ID types.String `tfsdk:"vpc1_id"`
	VPC2ID types.String `tfsdk:"vpc2_id"`
	Phase  types.String `tfsdk:"phase"`
}

// NewPeeringResource is the infra_peering resource factory.
func NewPeeringResource() resource.Resource {
	return newGeneric(resourceDef[peeringModel, infra.PeeringSpec, infra.PeeringStatus]{
		kind: infra.KindPeering,
		schema: schema.Schema{
			MarkdownDescription: "A peering between two VPCs.",
			Attributes: map[string]schema.Attribute{
				"id":      idAttribute(),
				"vpc1_id": schema.StringAttribute{Required: true, MarkdownDescription: "First VPC id."},
				"vpc2_id": schema.StringAttribute{Required: true, MarkdownDescription: "Second VPC id."},
				"phase":   phaseAttribute(),
			},
		},
		toSpec: func(_ context.Context, m peeringModel) (infra.PeeringSpec, diag.Diagnostics) {
			return infra.PeeringSpec{VPC1ID: m.VPC1ID.ValueString(), VPC2ID: m.VPC2ID.ValueString()}, nil
		},
		toModel: func(_ context.Context, r *infra.Peering) (peeringModel, diag.Diagnostics) {
			return peeringModel{
				ID:     types.StringValue(r.Metadata.UID),
				VPC1ID: types.StringValue(r.Spec.VPC1ID),
				VPC2ID: types.StringValue(r.Spec.VPC2ID),
				Phase:  types.StringValue(string(r.Status.Phase)),
			}, nil
		},
		id: func(m peeringModel) string { return m.ID.ValueString() },
	})
}
