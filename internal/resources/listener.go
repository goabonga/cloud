// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

// replacedBy marks the attribute naming what a resource belongs to: moving it
// elsewhere is a new resource, not an update.
func replacedBy() []planmodifier.String {
	return []planmodifier.String{stringplanmodifier.RequiresReplace()}
}

type healthCheckModel struct {
	Protocol           types.String `tfsdk:"protocol"`
	Path               types.String `tfsdk:"path"`
	Port               types.Int64  `tfsdk:"port"`
	IntervalSeconds    types.Int64  `tfsdk:"interval_seconds"`
	TimeoutSeconds     types.Int64  `tfsdk:"timeout_seconds"`
	HealthyThreshold   types.Int64  `tfsdk:"healthy_threshold"`
	UnhealthyThreshold types.Int64  `tfsdk:"unhealthy_threshold"`
}

var healthCheckAttrTypes = map[string]attr.Type{
	"protocol":            types.StringType,
	"path":                types.StringType,
	"port":                types.Int64Type,
	"interval_seconds":    types.Int64Type,
	"timeout_seconds":     types.Int64Type,
	"healthy_threshold":   types.Int64Type,
	"unhealthy_threshold": types.Int64Type,
}

type lbTargetGroupModel struct {
	ID          types.String `tfsdk:"id"`
	VPCID       types.String `tfsdk:"vpc_id"`
	Protocol    types.String `tfsdk:"protocol"`
	Port        types.Int64  `tfsdk:"port"`
	BackendCAID types.String `tfsdk:"backend_ca_id"`
	ServerName  types.String `tfsdk:"server_name"`
	HealthCheck types.Object `tfsdk:"health_check"`
	Phase       types.String `tfsdk:"phase"`
}

// NewLBTargetGroupResource is the infra_lb_target_group resource factory.
func NewLBTargetGroupResource() resource.Resource {
	optStr := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: desc}
	}
	optInt := func(desc string) schema.Int64Attribute {
		return schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: desc}
	}
	return newGeneric(resourceDef[lbTargetGroupModel, infra.LBTargetGroupSpec, infra.LBTargetGroupStatus]{
		kind: infra.KindLBTargetGroup,
		schema: schema.Schema{
			MarkdownDescription: "A pool of targets in a VPC that listeners route to, with the protocol and health check towards them.",
			Attributes: map[string]schema.Attribute{
				"id":            idAttribute(),
				"vpc_id":        schema.StringAttribute{Required: true, PlanModifiers: replacedBy(), MarkdownDescription: "VPC of the targets."},
				"protocol":      optStr("Protocol towards the targets: http (default), https or tcp."),
				"port":          schema.Int64Attribute{Required: true, MarkdownDescription: "Targets' port, unless a target names its own."},
				"backend_ca_id": optStr("With https, the CA verifying the targets' certificates; empty trusts the platform's global CAs."),
				"server_name":   optStr("With https, the name the targets' certificates are verified against and asked for by SNI; empty uses the request's host."),
				"health_check": schema.SingleNestedAttribute{
					Optional:            true,
					Computed:            true,
					MarkdownDescription: "How the targets are probed; every field has a default.",
					Attributes: map[string]schema.Attribute{
						"protocol":            optStr("http, https or tcp; the group's protocol by default."),
						"path":                optStr("Path probed; / by default, none for tcp."),
						"port":                optInt("Port probed; 0, the default, probes the traffic port."),
						"interval_seconds":    optInt("Seconds between probes; 5 by default."),
						"timeout_seconds":     optInt("Probe timeout, shorter than the interval; 2 by default."),
						"healthy_threshold":   optInt("Successes before a target is healthy; 2 by default."),
						"unhealthy_threshold": optInt("Failures before a target is unhealthy; 3 by default."),
					},
				},
				"phase": phaseAttribute(),
			},
		},
		toSpec: func(ctx context.Context, m lbTargetGroupModel) (infra.LBTargetGroupSpec, diag.Diagnostics) {
			var diags diag.Diagnostics
			spec := infra.LBTargetGroupSpec{
				VPCID:       m.VPCID.ValueString(),
				Protocol:    m.Protocol.ValueString(),
				Port:        int(m.Port.ValueInt64()),
				BackendCAID: m.BackendCAID.ValueString(),
				ServerName:  m.ServerName.ValueString(),
			}
			if !m.HealthCheck.IsNull() && !m.HealthCheck.IsUnknown() {
				var hc healthCheckModel
				diags.Append(m.HealthCheck.As(ctx, &hc, basetypes.ObjectAsOptions{UnhandledUnknownAsEmpty: true})...)
				spec.HealthCheck = infra.LBHealthCheck{
					Protocol:           hc.Protocol.ValueString(),
					Path:               hc.Path.ValueString(),
					Port:               int(hc.Port.ValueInt64()),
					IntervalSeconds:    int(hc.IntervalSeconds.ValueInt64()),
					TimeoutSeconds:     int(hc.TimeoutSeconds.ValueInt64()),
					HealthyThreshold:   int(hc.HealthyThreshold.ValueInt64()),
					UnhealthyThreshold: int(hc.UnhealthyThreshold.ValueInt64()),
				}
			}
			return spec, diags
		},
		toModel: func(ctx context.Context, r *infra.LBTargetGroup) (lbTargetGroupModel, diag.Diagnostics) {
			hc := r.Spec.HealthCheck
			obj, diags := types.ObjectValueFrom(ctx, healthCheckAttrTypes, healthCheckModel{
				Protocol:           types.StringValue(hc.Protocol),
				Path:               types.StringValue(hc.Path),
				Port:               types.Int64Value(int64(hc.Port)),
				IntervalSeconds:    types.Int64Value(int64(hc.IntervalSeconds)),
				TimeoutSeconds:     types.Int64Value(int64(hc.TimeoutSeconds)),
				HealthyThreshold:   types.Int64Value(int64(hc.HealthyThreshold)),
				UnhealthyThreshold: types.Int64Value(int64(hc.UnhealthyThreshold)),
			})
			return lbTargetGroupModel{
				ID:          types.StringValue(r.Metadata.UID),
				VPCID:       types.StringValue(r.Spec.VPCID),
				Protocol:    types.StringValue(r.Spec.Protocol),
				Port:        types.Int64Value(int64(r.Spec.Port)),
				BackendCAID: types.StringValue(r.Spec.BackendCAID),
				ServerName:  types.StringValue(r.Spec.ServerName),
				HealthCheck: obj,
				Phase:       types.StringValue(string(r.Status.Phase)),
			}, diags
		},
		id: func(m lbTargetGroupModel) string { return m.ID.ValueString() },
	})
}

type lbTargetModel struct {
	ID            types.String `tfsdk:"id"`
	TargetGroupID types.String `tfsdk:"target_group_id"`
	ComputeID     types.String `tfsdk:"compute_id"`
	Port          types.Int64  `tfsdk:"port"`
	Weight        types.Int64  `tfsdk:"weight"`
	Phase         types.String `tfsdk:"phase"`
}

// NewLBTargetResource is the infra_lb_target resource factory.
func NewLBTargetResource() resource.Resource {
	return newGeneric(resourceDef[lbTargetModel, infra.LBTargetSpec, infra.LBTargetStatus]{
		kind: infra.KindLBTarget,
		schema: schema.Schema{
			MarkdownDescription: "A compute instance attached to a target group.",
			Attributes: map[string]schema.Attribute{
				"id":              idAttribute(),
				"target_group_id": schema.StringAttribute{Required: true, PlanModifiers: replacedBy(), MarkdownDescription: "Target group id."},
				"compute_id":      schema.StringAttribute{Required: true, PlanModifiers: replacedBy(), MarkdownDescription: "Compute instance id."},
				"port":            schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "Port overriding the group's; 0 keeps it."},
				"weight":          schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "Share of the traffic, 1 to 1000; 1 by default."},
				"phase":           phaseAttribute(),
			},
		},
		toSpec: func(_ context.Context, m lbTargetModel) (infra.LBTargetSpec, diag.Diagnostics) {
			return infra.LBTargetSpec{
				TargetGroupID: m.TargetGroupID.ValueString(),
				ComputeID:     m.ComputeID.ValueString(),
				Port:          int(m.Port.ValueInt64()),
				Weight:        int(m.Weight.ValueInt64()),
			}, nil
		},
		toModel: func(_ context.Context, r *infra.LBTarget) (lbTargetModel, diag.Diagnostics) {
			return lbTargetModel{
				ID:            types.StringValue(r.Metadata.UID),
				TargetGroupID: types.StringValue(r.Spec.TargetGroupID),
				ComputeID:     types.StringValue(r.Spec.ComputeID),
				Port:          types.Int64Value(int64(r.Spec.Port)),
				Weight:        types.Int64Value(int64(r.Spec.Weight)),
				Phase:         types.StringValue(string(r.Status.Phase)),
			}, nil
		},
		id: func(m lbTargetModel) string { return m.ID.ValueString() },
	})
}

type listenerRuleModel struct {
	Host          types.String `tfsdk:"host"`
	PathPrefix    types.String `tfsdk:"path_prefix"`
	TargetGroupID types.String `tfsdk:"target_group_id"`
}

var listenerRuleObjectType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"host":            types.StringType,
	"path_prefix":     types.StringType,
	"target_group_id": types.StringType,
}}

type lbListenerModel struct {
	ID                   types.String `tfsdk:"id"`
	LoadBalancerID       types.String `tfsdk:"load_balancer_id"`
	Port                 types.Int64  `tfsdk:"port"`
	Protocol             types.String `tfsdk:"protocol"`
	TLSMode              types.String `tfsdk:"tls_mode"`
	CertificateIDs       types.List   `tfsdk:"certificate_ids"`
	DefaultTargetGroupID types.String `tfsdk:"default_target_group_id"`
	Rules                types.List   `tfsdk:"rules"`
	Phase                types.String `tfsdk:"phase"`
}

// NewLBListenerResource is the infra_lb_listener resource factory.
func NewLBListenerResource() resource.Resource {
	return newGeneric(resourceDef[lbListenerModel, infra.LBListenerSpec, infra.LBListenerStatus]{
		kind: infra.KindLBListener,
		schema: schema.Schema{
			MarkdownDescription: "A port of a load balancer accepting http, https, tls or tcp, routing to target groups " +
				"by host and path - or by SNI for tls passthrough.",
			Attributes: map[string]schema.Attribute{
				"id":               idAttribute(),
				"load_balancer_id": schema.StringAttribute{Required: true, PlanModifiers: replacedBy(), MarkdownDescription: "Load balancer id."},
				"port":             schema.Int64Attribute{Required: true, MarkdownDescription: "Listening port."},
				"protocol":         schema.StringAttribute{Required: true, MarkdownDescription: "http, https, tls or tcp."},
				"tls_mode": schema.StringAttribute{Optional: true, Computed: true,
					MarkdownDescription: "terminate (default) or reencrypt with https; passthrough with tls."},
				"certificate_ids": schema.ListAttribute{Optional: true, Computed: true, ElementType: types.StringType,
					MarkdownDescription: "`infra_ssl_cert`s an https listener serves, picked by SNI."},
				"default_target_group_id": schema.StringAttribute{Required: true, MarkdownDescription: "Target group of what no rule matches."},
				"rules": schema.ListNestedAttribute{
					Optional:            true,
					Computed:            true,
					MarkdownDescription: "Routes by host and path prefix, or by SNI host on a tls listener.",
					NestedObject: schema.NestedAttributeObject{
						Attributes: map[string]schema.Attribute{
							"host":            schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Host (or SNI) matched."},
							"path_prefix":     schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Path prefix matched, starting with /."},
							"target_group_id": schema.StringAttribute{Required: true, MarkdownDescription: "Target group routed to."},
						},
					},
				},
				"phase": phaseAttribute(),
			},
		},
		toSpec: func(ctx context.Context, m lbListenerModel) (infra.LBListenerSpec, diag.Diagnostics) {
			var diags diag.Diagnostics
			spec := infra.LBListenerSpec{
				LoadBalancerID:       m.LoadBalancerID.ValueString(),
				Port:                 int(m.Port.ValueInt64()),
				Protocol:             m.Protocol.ValueString(),
				TLSMode:              m.TLSMode.ValueString(),
				DefaultTargetGroupID: m.DefaultTargetGroupID.ValueString(),
			}
			if !m.CertificateIDs.IsNull() && !m.CertificateIDs.IsUnknown() {
				diags.Append(m.CertificateIDs.ElementsAs(ctx, &spec.CertificateIDs, false)...)
			}
			if !m.Rules.IsNull() && !m.Rules.IsUnknown() {
				var rules []listenerRuleModel
				diags.Append(m.Rules.ElementsAs(ctx, &rules, false)...)
				for _, r := range rules {
					spec.Rules = append(spec.Rules, infra.LBListenerRule{
						Host:          r.Host.ValueString(),
						PathPrefix:    r.PathPrefix.ValueString(),
						TargetGroupID: r.TargetGroupID.ValueString(),
					})
				}
			}
			return spec, diags
		},
		toModel: func(ctx context.Context, r *infra.LBListener) (lbListenerModel, diag.Diagnostics) {
			var diags diag.Diagnostics
			certs, d := types.ListValueFrom(ctx, types.StringType, r.Spec.CertificateIDs)
			diags.Append(d...)
			rules := make([]listenerRuleModel, 0, len(r.Spec.Rules))
			for _, rule := range r.Spec.Rules {
				rules = append(rules, listenerRuleModel{
					Host:          types.StringValue(rule.Host),
					PathPrefix:    types.StringValue(rule.PathPrefix),
					TargetGroupID: types.StringValue(rule.TargetGroupID),
				})
			}
			rulesList, d := types.ListValueFrom(ctx, listenerRuleObjectType, rules)
			diags.Append(d...)
			return lbListenerModel{
				ID:                   types.StringValue(r.Metadata.UID),
				LoadBalancerID:       types.StringValue(r.Spec.LoadBalancerID),
				Port:                 types.Int64Value(int64(r.Spec.Port)),
				Protocol:             types.StringValue(r.Spec.Protocol),
				TLSMode:              types.StringValue(r.Spec.TLSMode),
				CertificateIDs:       certs,
				DefaultTargetGroupID: types.StringValue(r.Spec.DefaultTargetGroupID),
				Rules:                rulesList,
				Phase:                types.StringValue(string(r.Status.Phase)),
			}, diags
		},
		id: func(m lbListenerModel) string { return m.ID.ValueString() },
	})
}
