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

type subnetModel struct {
	ID      types.String `tfsdk:"id"`
	VPCID   types.String `tfsdk:"vpc_id"`
	CIDR    types.String `tfsdk:"cidr"`
	Type    types.String `tfsdk:"type"`
	Gateway types.String `tfsdk:"gateway"`
	Phase   types.String `tfsdk:"phase"`
}

// NewSubnetResource is the infra_subnet resource factory.
func NewSubnetResource() resource.Resource {
	return newGeneric(resourceDef[subnetModel, infra.SubnetSpec, infra.SubnetStatus]{
		kind: infra.KindSubnet,
		schema: schema.Schema{
			MarkdownDescription: "A subnet within a VPC.",
			Attributes: map[string]schema.Attribute{
				"id":      idAttribute(),
				"vpc_id":  schema.StringAttribute{Required: true, MarkdownDescription: "Parent VPC id."},
				"cidr":    schema.StringAttribute{Required: true, MarkdownDescription: "Subnet range, e.g. 10.0.1.0/24."},
				"type":    schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "public or private."},
				"gateway": schema.StringAttribute{Computed: true, MarkdownDescription: "Gateway address assigned by the agent."},
				"phase":   phaseAttribute(),
			},
		},
		toSpec: func(_ context.Context, m subnetModel) (infra.SubnetSpec, diag.Diagnostics) {
			return infra.SubnetSpec{VPCID: m.VPCID.ValueString(), CIDR: m.CIDR.ValueString(), Type: m.Type.ValueString()}, nil
		},
		toModel: func(_ context.Context, r *infra.Subnet) (subnetModel, diag.Diagnostics) {
			return subnetModel{
				ID:      types.StringValue(r.Metadata.UID),
				VPCID:   types.StringValue(r.Spec.VPCID),
				CIDR:    types.StringValue(r.Spec.CIDR),
				Type:    types.StringValue(r.Spec.Type),
				Gateway: types.StringValue(r.Status.Gateway),
				Phase:   types.StringValue(string(r.Status.Phase)),
			}, nil
		},
		id: func(m subnetModel) string { return m.ID.ValueString() },
	})
}
