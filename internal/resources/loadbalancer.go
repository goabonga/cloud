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

type loadBalancerModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	VPCID     types.String `tfsdk:"vpc_id"`
	Address   types.String `tfsdk:"address"`
	Port      types.Int64  `tfsdk:"port"`
	Protocol  types.String `tfsdk:"protocol"`
	Algorithm types.String `tfsdk:"algorithm"`
	ServiceID types.String `tfsdk:"service_id"`
	Phase     types.String `tfsdk:"phase"`
	// Public reachability through the edges.
	PublicIPID    types.String `tfsdk:"public_ip_id"`
	PublicAddress types.String `tfsdk:"public_address"`
}

// NewLoadBalancerResource is the infra_load_balancer resource factory.
func NewLoadBalancerResource() resource.Resource {
	return newGeneric(resourceDef[loadBalancerModel, infra.LoadBalancerSpec, infra.LoadBalancerStatus]{
		kind: infra.KindLoadBalancer,
		schema: schema.Schema{
			MarkdownDescription: "A layer-4 load balancer fronting compute backends.",
			Attributes: map[string]schema.Attribute{
				"id":             idAttribute(),
				"name":           schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Display name."},
				"vpc_id":         schema.StringAttribute{Required: true, MarkdownDescription: "Parent VPC id."},
				"address":        schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Virtual address; assigned when omitted."},
				"port":           schema.Int64Attribute{Required: true, MarkdownDescription: "Listening port."},
				"protocol":       schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "tcp or udp."},
				"algorithm":      schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "round_robin, least_conn or source."},
				"service_id":     schema.StringAttribute{Computed: true, MarkdownDescription: "Backend service identifier."},
				"public_ip_id":   schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "A public `infra_ip_address` the load balancer is also reachable on from outside the VPC, through the edges."},
				"public_address": schema.StringAttribute{Computed: true, MarkdownDescription: "The public address the edges serve the load balancer on."},
				"phase":          phaseAttribute(),
			},
		},
		toSpec: func(_ context.Context, m loadBalancerModel) (infra.LoadBalancerSpec, diag.Diagnostics) {
			return infra.LoadBalancerSpec{
				Name:       m.Name.ValueString(),
				VPCID:      m.VPCID.ValueString(),
				Address:    m.Address.ValueString(),
				Port:       int(m.Port.ValueInt64()),
				Protocol:   m.Protocol.ValueString(),
				Algorithm:  m.Algorithm.ValueString(),
				PublicIPID: m.PublicIPID.ValueString(),
			}, nil
		},
		toModel: func(_ context.Context, r *infra.LoadBalancer) (loadBalancerModel, diag.Diagnostics) {
			address := r.Spec.Address
			if r.Status.Address != "" {
				address = r.Status.Address
			}
			return loadBalancerModel{
				ID:            types.StringValue(r.Metadata.UID),
				Name:          types.StringValue(r.Spec.Name),
				VPCID:         types.StringValue(r.Spec.VPCID),
				Address:       types.StringValue(address),
				Port:          types.Int64Value(int64(r.Spec.Port)),
				Protocol:      types.StringValue(r.Spec.Protocol),
				Algorithm:     types.StringValue(r.Spec.Algorithm),
				ServiceID:     types.StringValue(r.Status.ServiceID),
				Phase:         types.StringValue(string(r.Status.Phase)),
				PublicIPID:    types.StringValue(r.Spec.PublicIPID),
				PublicAddress: types.StringValue(r.Status.PublicAddress),
			}, nil
		},
		id: func(m loadBalancerModel) string { return m.ID.ValueString() },
	})
}

type lbBackendModel struct {
	ID           types.String `tfsdk:"id"`
	LBID         types.String `tfsdk:"lb_id"`
	ComputeID    types.String `tfsdk:"compute_id"`
	Port         types.Int64  `tfsdk:"port"`
	Weight       types.Int64  `tfsdk:"weight"`
	RealServerIP types.String `tfsdk:"real_server_ip"`
	Phase        types.String `tfsdk:"phase"`
}

// NewLBBackendResource is the infra_lb_backend resource factory.
func NewLBBackendResource() resource.Resource {
	return newGeneric(resourceDef[lbBackendModel, infra.LBBackendSpec, infra.LBBackendStatus]{
		kind: infra.KindLBBackend,
		schema: schema.Schema{
			MarkdownDescription: "A compute backend attached to a load balancer.",
			Attributes: map[string]schema.Attribute{
				"id":             idAttribute(),
				"lb_id":          schema.StringAttribute{Required: true, MarkdownDescription: "Parent load balancer id."},
				"compute_id":     schema.StringAttribute{Required: true, MarkdownDescription: "Backend compute id."},
				"port":           schema.Int64Attribute{Required: true, MarkdownDescription: "Backend port."},
				"weight":         schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "Relative weight."},
				"real_server_ip": schema.StringAttribute{Computed: true, MarkdownDescription: "Resolved backend address."},
				"phase":          phaseAttribute(),
			},
		},
		toSpec: func(_ context.Context, m lbBackendModel) (infra.LBBackendSpec, diag.Diagnostics) {
			return infra.LBBackendSpec{
				LBID:      m.LBID.ValueString(),
				ComputeID: m.ComputeID.ValueString(),
				Port:      int(m.Port.ValueInt64()),
				Weight:    int(m.Weight.ValueInt64()),
			}, nil
		},
		toModel: func(_ context.Context, r *infra.LBBackend) (lbBackendModel, diag.Diagnostics) {
			return lbBackendModel{
				ID:           types.StringValue(r.Metadata.UID),
				LBID:         types.StringValue(r.Spec.LBID),
				ComputeID:    types.StringValue(r.Spec.ComputeID),
				Port:         types.Int64Value(int64(r.Spec.Port)),
				Weight:       types.Int64Value(int64(r.Spec.Weight)),
				RealServerIP: types.StringValue(r.Status.RealServerIP),
				Phase:        types.StringValue(string(r.Status.Phase)),
			}, nil
		},
		id: func(m lbBackendModel) string { return m.ID.ValueString() },
	})
}
