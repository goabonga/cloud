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

type securityGroupModel struct {
	ID    types.String `tfsdk:"id"`
	VPCID types.String `tfsdk:"vpc_id"`
	Name  types.String `tfsdk:"name"`
	Phase types.String `tfsdk:"phase"`
}

// NewSecurityGroupResource is the infra_security_group resource factory.
func NewSecurityGroupResource() resource.Resource {
	return newGeneric(resourceDef[securityGroupModel, infra.SecurityGroupSpec, infra.SecurityGroupStatus]{
		kind: infra.KindSecurityGroup,
		schema: schema.Schema{
			MarkdownDescription: "A security group (firewall) attached to a VPC.",
			Attributes: map[string]schema.Attribute{
				"id":     idAttribute(),
				"vpc_id": schema.StringAttribute{Required: true, MarkdownDescription: "Parent VPC id."},
				"name":   schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Display name."},
				"phase":  phaseAttribute(),
			},
		},
		toSpec: func(_ context.Context, m securityGroupModel) (infra.SecurityGroupSpec, diag.Diagnostics) {
			return infra.SecurityGroupSpec{VPCID: m.VPCID.ValueString(), Name: m.Name.ValueString()}, nil
		},
		toModel: func(_ context.Context, r *infra.SecurityGroup) (securityGroupModel, diag.Diagnostics) {
			return securityGroupModel{
				ID:    types.StringValue(r.Metadata.UID),
				VPCID: types.StringValue(r.Spec.VPCID),
				Name:  types.StringValue(r.Spec.Name),
				Phase: types.StringValue(string(r.Status.Phase)),
			}, nil
		},
		id: func(m securityGroupModel) string { return m.ID.ValueString() },
	})
}

type securityGroupRuleModel struct {
	ID              types.String `tfsdk:"id"`
	SecurityGroupID types.String `tfsdk:"security_group_id"`
	Direction       types.String `tfsdk:"direction"`
	Protocol        types.String `tfsdk:"protocol"`
	Port            types.Int64  `tfsdk:"port"`
	CIDR            types.String `tfsdk:"cidr"`
	Phase           types.String `tfsdk:"phase"`
}

// NewSecurityGroupRuleResource is the infra_security_group_rule resource factory.
func NewSecurityGroupRuleResource() resource.Resource {
	return newGeneric(resourceDef[securityGroupRuleModel, infra.SecurityGroupRuleSpec, infra.SecurityGroupRuleStatus]{
		kind: infra.KindSecurityGroupRule,
		schema: schema.Schema{
			MarkdownDescription: "A rule within a security group.",
			Attributes: map[string]schema.Attribute{
				"id":                idAttribute(),
				"security_group_id": schema.StringAttribute{Required: true, MarkdownDescription: "Parent security group id."},
				"direction":         schema.StringAttribute{Required: true, MarkdownDescription: "ingress or egress."},
				"protocol":          schema.StringAttribute{Required: true, MarkdownDescription: "tcp, udp, icmp or all."},
				"port":              schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "Destination port (tcp/udp)."},
				"cidr":              schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Source CIDR."},
				"phase":             phaseAttribute(),
			},
		},
		toSpec: func(_ context.Context, m securityGroupRuleModel) (infra.SecurityGroupRuleSpec, diag.Diagnostics) {
			return infra.SecurityGroupRuleSpec{
				SecurityGroupID: m.SecurityGroupID.ValueString(),
				Direction:       m.Direction.ValueString(),
				Protocol:        m.Protocol.ValueString(),
				Port:            int(m.Port.ValueInt64()),
				CIDR:            m.CIDR.ValueString(),
			}, nil
		},
		toModel: func(_ context.Context, r *infra.SecurityGroupRule) (securityGroupRuleModel, diag.Diagnostics) {
			return securityGroupRuleModel{
				ID:              types.StringValue(r.Metadata.UID),
				SecurityGroupID: types.StringValue(r.Spec.SecurityGroupID),
				Direction:       types.StringValue(r.Spec.Direction),
				Protocol:        types.StringValue(r.Spec.Protocol),
				Port:            types.Int64Value(int64(r.Spec.Port)),
				CIDR:            types.StringValue(r.Spec.CIDR),
				Phase:           types.StringValue(string(r.Status.Phase)),
			}, nil
		},
		id: func(m securityGroupRuleModel) string { return m.ID.ValueString() },
	})
}
