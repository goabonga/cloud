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

type vpcModel struct {
	ID         types.String `tfsdk:"id"`
	CIDR       types.String `tfsdk:"cidr"`
	BridgeName types.String `tfsdk:"bridge_name"`
	Phase      types.String `tfsdk:"phase"`
}

// NewVPCResource is the infra_vpc resource factory.
func NewVPCResource() resource.Resource {
	return newGeneric(resourceDef[vpcModel, infra.VPCSpec, infra.VPCStatus]{
		kind: infra.KindVPC,
		schema: schema.Schema{
			MarkdownDescription: "A virtual private cloud: an isolated Linux bridge fabric.",
			Attributes: map[string]schema.Attribute{
				"id":          idAttribute(),
				"cidr":        schema.StringAttribute{Required: true, MarkdownDescription: "Address range in CIDR notation, e.g. 10.0.0.0/16."},
				"bridge_name": schema.StringAttribute{Computed: true, MarkdownDescription: "Linux bridge backing the VPC."},
				"phase":       schema.StringAttribute{Computed: true, MarkdownDescription: "Lifecycle phase reported by the control plane."},
			},
		},
		toSpec: func(_ context.Context, m vpcModel) (infra.VPCSpec, diag.Diagnostics) {
			return infra.VPCSpec{CIDR: m.CIDR.ValueString()}, nil
		},
		toModel: func(_ context.Context, r *infra.VPC) (vpcModel, diag.Diagnostics) {
			return vpcModel{
				ID:         types.StringValue(r.Metadata.UID),
				CIDR:       types.StringValue(r.Spec.CIDR),
				BridgeName: types.StringValue(r.Status.BridgeName),
				Phase:      types.StringValue(string(r.Status.Phase)),
			}, nil
		},
		id: func(m vpcModel) string { return m.ID.ValueString() },
	})
}
