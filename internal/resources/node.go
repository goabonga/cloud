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

type nodeModel struct {
	ID              types.String `tfsdk:"id"`
	Hostname        types.String `tfsdk:"hostname"`
	Address         types.String `tfsdk:"address"`
	Labels          types.Map    `tfsdk:"labels"`
	CapacityCPUs    types.Int64  `tfsdk:"capacity_cpus"`
	CapacityMemory  types.Int64  `tfsdk:"capacity_memory_mb"`
	CapacityMaxPods types.Int64  `tfsdk:"capacity_max_pods"`
	LastSeen        types.String `tfsdk:"last_seen"`
	Phase           types.String `tfsdk:"phase"`
}

// NewNodeResource is the infra_node resource factory.
func NewNodeResource() resource.Resource {
	return newGeneric(resourceDef[nodeModel, infra.NodeSpec, infra.NodeStatus]{
		kind: infra.KindNode,
		schema: schema.Schema{
			MarkdownDescription: "A host registered with the control plane.",
			Attributes: map[string]schema.Attribute{
				"id":                 idAttribute(),
				"hostname":           schema.StringAttribute{Required: true, MarkdownDescription: "Node hostname."},
				"address":            schema.StringAttribute{Required: true, MarkdownDescription: "Node address."},
				"labels":             schema.MapAttribute{Optional: true, Computed: true, ElementType: types.StringType, MarkdownDescription: "Scheduling labels."},
				"capacity_cpus":      schema.Int64Attribute{Required: true, MarkdownDescription: "Schedulable CPUs."},
				"capacity_memory_mb": schema.Int64Attribute{Required: true, MarkdownDescription: "Schedulable memory in MB."},
				"capacity_max_pods":  schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "Maximum pods."},
				"last_seen":          schema.StringAttribute{Computed: true, MarkdownDescription: "Last heartbeat timestamp."},
				"phase":              phaseAttribute(),
			},
		},
		toSpec: func(ctx context.Context, m nodeModel) (infra.NodeSpec, diag.Diagnostics) {
			var diags diag.Diagnostics
			var labels map[string]string
			if !m.Labels.IsNull() && !m.Labels.IsUnknown() {
				diags.Append(m.Labels.ElementsAs(ctx, &labels, false)...)
			}
			return infra.NodeSpec{
				Hostname: m.Hostname.ValueString(),
				Address:  m.Address.ValueString(),
				Labels:   labels,
				Capacity: infra.NodeCapacity{
					CPUs:     int(m.CapacityCPUs.ValueInt64()),
					MemoryMB: int(m.CapacityMemory.ValueInt64()),
					MaxPods:  int(m.CapacityMaxPods.ValueInt64()),
				},
			}, diags
		},
		toModel: func(ctx context.Context, r *infra.Node) (nodeModel, diag.Diagnostics) {
			labels, d := types.MapValueFrom(ctx, types.StringType, r.Spec.Labels)
			return nodeModel{
				ID:              types.StringValue(r.Metadata.UID),
				Hostname:        types.StringValue(r.Spec.Hostname),
				Address:         types.StringValue(r.Spec.Address),
				Labels:          labels,
				CapacityCPUs:    types.Int64Value(int64(r.Spec.Capacity.CPUs)),
				CapacityMemory:  types.Int64Value(int64(r.Spec.Capacity.MemoryMB)),
				CapacityMaxPods: types.Int64Value(int64(r.Spec.Capacity.MaxPods)),
				LastSeen:        types.StringValue(r.Status.LastSeen),
				Phase:           types.StringValue(string(r.Status.Phase)),
			}, d
		},
		id: func(m nodeModel) string { return m.ID.ValueString() },
	})
}

type nodePoolModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	NodeSelector types.Map    `tfsdk:"node_selector"`
	MinNodes     types.Int64  `tfsdk:"min_nodes"`
	MaxNodes     types.Int64  `tfsdk:"max_nodes"`
	ReadyNodes   types.Int64  `tfsdk:"ready_nodes"`
	TotalNodes   types.Int64  `tfsdk:"total_nodes"`
	Phase        types.String `tfsdk:"phase"`
}

// NewNodePoolResource is the infra_node_pool resource factory.
func NewNodePoolResource() resource.Resource {
	return newGeneric(resourceDef[nodePoolModel, infra.NodePoolSpec, infra.NodePoolStatus]{
		kind: infra.KindNodePool,
		schema: schema.Schema{
			MarkdownDescription: "A pool of nodes selected by label.",
			Attributes: map[string]schema.Attribute{
				"id":            idAttribute(),
				"name":          schema.StringAttribute{Required: true, MarkdownDescription: "Pool name."},
				"node_selector": schema.MapAttribute{Optional: true, Computed: true, ElementType: types.StringType, MarkdownDescription: "Labels selecting member nodes."},
				"min_nodes":     schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "Minimum nodes."},
				"max_nodes":     schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "Maximum nodes."},
				"ready_nodes":   schema.Int64Attribute{Computed: true, MarkdownDescription: "Ready member nodes."},
				"total_nodes":   schema.Int64Attribute{Computed: true, MarkdownDescription: "Total member nodes."},
				"phase":         phaseAttribute(),
			},
		},
		toSpec: func(ctx context.Context, m nodePoolModel) (infra.NodePoolSpec, diag.Diagnostics) {
			var diags diag.Diagnostics
			var selector map[string]string
			if !m.NodeSelector.IsNull() && !m.NodeSelector.IsUnknown() {
				diags.Append(m.NodeSelector.ElementsAs(ctx, &selector, false)...)
			}
			return infra.NodePoolSpec{
				Name:         m.Name.ValueString(),
				NodeSelector: selector,
				MinNodes:     int(m.MinNodes.ValueInt64()),
				MaxNodes:     int(m.MaxNodes.ValueInt64()),
			}, diags
		},
		toModel: func(ctx context.Context, r *infra.NodePool) (nodePoolModel, diag.Diagnostics) {
			selector, d := types.MapValueFrom(ctx, types.StringType, r.Spec.NodeSelector)
			return nodePoolModel{
				ID:           types.StringValue(r.Metadata.UID),
				Name:         types.StringValue(r.Spec.Name),
				NodeSelector: selector,
				MinNodes:     types.Int64Value(int64(r.Spec.MinNodes)),
				MaxNodes:     types.Int64Value(int64(r.Spec.MaxNodes)),
				ReadyNodes:   types.Int64Value(int64(r.Status.ReadyNodes)),
				TotalNodes:   types.Int64Value(int64(r.Status.TotalNodes)),
				Phase:        types.StringValue(string(r.Status.Phase)),
			}, d
		},
		id: func(m nodePoolModel) string { return m.ID.ValueString() },
	})
}
