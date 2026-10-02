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

type wafPolicyModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	TargetType types.String `tfsdk:"target_type"`
	TargetID   types.String `tfsdk:"target_id"`
	LogEnabled types.Bool   `tfsdk:"log_enabled"`
	Chain      types.String `tfsdk:"chain"`
	Phase      types.String `tfsdk:"phase"`
}

// NewWAFPolicyResource is the infra_waf_policy resource factory.
func NewWAFPolicyResource() resource.Resource {
	return newGeneric(resourceDef[wafPolicyModel, infra.WAFPolicySpec, infra.WAFPolicyStatus]{
		kind: infra.KindWAFPolicy,
		schema: schema.Schema{
			MarkdownDescription: "A web-application-firewall policy attached to a target.",
			Attributes: map[string]schema.Attribute{
				"id":          idAttribute(),
				"name":        schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Display name."},
				"target_type": schema.StringAttribute{Required: true, MarkdownDescription: "igw, subnet or compute."},
				"target_id":   schema.StringAttribute{Required: true, MarkdownDescription: "Target resource id."},
				"log_enabled": schema.BoolAttribute{Optional: true, Computed: true, MarkdownDescription: "Log matched traffic."},
				"chain":       schema.StringAttribute{Computed: true, MarkdownDescription: "iptables chain assigned by the agent."},
				"phase":       phaseAttribute(),
			},
		},
		toSpec: func(_ context.Context, m wafPolicyModel) (infra.WAFPolicySpec, diag.Diagnostics) {
			return infra.WAFPolicySpec{
				Name:       m.Name.ValueString(),
				TargetType: m.TargetType.ValueString(),
				TargetID:   m.TargetID.ValueString(),
				LogEnabled: m.LogEnabled.ValueBool(),
			}, nil
		},
		toModel: func(_ context.Context, r *infra.WAFPolicy) (wafPolicyModel, diag.Diagnostics) {
			return wafPolicyModel{
				ID:         types.StringValue(r.Metadata.UID),
				Name:       types.StringValue(r.Spec.Name),
				TargetType: types.StringValue(r.Spec.TargetType),
				TargetID:   types.StringValue(r.Spec.TargetID),
				LogEnabled: types.BoolValue(r.Spec.LogEnabled),
				Chain:      types.StringValue(r.Status.Chain),
				Phase:      types.StringValue(string(r.Status.Phase)),
			}, nil
		},
		id: func(m wafPolicyModel) string { return m.ID.ValueString() },
	})
}

type wafRuleModel struct {
	ID         types.String `tfsdk:"id"`
	PolicyID   types.String `tfsdk:"policy_id"`
	Priority   types.Int64  `tfsdk:"priority"`
	MatchType  types.String `tfsdk:"match_type"`
	MatchValue types.String `tfsdk:"match_value"`
	Action     types.String `tfsdk:"action"`
	RateLimit  types.Int64  `tfsdk:"rate_limit"`
	RateWindow types.Int64  `tfsdk:"rate_window"`
	Phase      types.String `tfsdk:"phase"`
}

// NewWAFRuleResource is the infra_waf_rule resource factory.
func NewWAFRuleResource() resource.Resource {
	return newGeneric(resourceDef[wafRuleModel, infra.WAFRuleSpec, infra.WAFRuleStatus]{
		kind: infra.KindWAFRule,
		schema: schema.Schema{
			MarkdownDescription: "A rule within a WAF policy.",
			Attributes: map[string]schema.Attribute{
				"id":          idAttribute(),
				"policy_id":   schema.StringAttribute{Required: true, MarkdownDescription: "Parent policy id."},
				"priority":    schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "Evaluation priority."},
				"match_type":  schema.StringAttribute{Required: true, MarkdownDescription: "What to match, e.g. path, header, ip."},
				"match_value": schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Value to match."},
				"action":      schema.StringAttribute{Required: true, MarkdownDescription: "block, allow, log or ratelimit."},
				"rate_limit":  schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "Requests allowed per window."},
				"rate_window": schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "Rate-limit window in seconds."},
				"phase":       phaseAttribute(),
			},
		},
		toSpec: func(_ context.Context, m wafRuleModel) (infra.WAFRuleSpec, diag.Diagnostics) {
			return infra.WAFRuleSpec{
				PolicyID:   m.PolicyID.ValueString(),
				Priority:   int(m.Priority.ValueInt64()),
				MatchType:  m.MatchType.ValueString(),
				MatchValue: m.MatchValue.ValueString(),
				Action:     m.Action.ValueString(),
				RateLimit:  int(m.RateLimit.ValueInt64()),
				RateWindow: int(m.RateWindow.ValueInt64()),
			}, nil
		},
		toModel: func(_ context.Context, r *infra.WAFRule) (wafRuleModel, diag.Diagnostics) {
			return wafRuleModel{
				ID:         types.StringValue(r.Metadata.UID),
				PolicyID:   types.StringValue(r.Spec.PolicyID),
				Priority:   types.Int64Value(int64(r.Spec.Priority)),
				MatchType:  types.StringValue(r.Spec.MatchType),
				MatchValue: types.StringValue(r.Spec.MatchValue),
				Action:     types.StringValue(r.Spec.Action),
				RateLimit:  types.Int64Value(int64(r.Spec.RateLimit)),
				RateWindow: types.Int64Value(int64(r.Spec.RateWindow)),
				Phase:      types.StringValue(string(r.Status.Phase)),
			}, nil
		},
		id: func(m wafRuleModel) string { return m.ID.ValueString() },
	})
}
