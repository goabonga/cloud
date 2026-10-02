// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

type warmPoolModel struct {
	MinWarm        types.Int64 `tfsdk:"min_warm"`
	MaxWarm        types.Int64 `tfsdk:"max_warm"`
	IdleTTLSeconds types.Int64 `tfsdk:"idle_ttl_seconds"`
	AllowColdStart types.Bool  `tfsdk:"allow_cold_start"`
}

var warmPoolAttrTypes = map[string]attr.Type{
	"min_warm":         types.Int64Type,
	"max_warm":         types.Int64Type,
	"idle_ttl_seconds": types.Int64Type,
	"allow_cold_start": types.BoolType,
}

type functionModel struct {
	ID              types.String  `tfsdk:"id"`
	Name            types.String  `tfsdk:"name"`
	SubnetID        types.String  `tfsdk:"subnet_id"`
	SecurityGroupID types.String  `tfsdk:"security_group_id"`
	NodePoolID      types.String  `tfsdk:"node_pool_id"`
	CPU             types.Float64 `tfsdk:"cpu"`
	MemoryMB        types.Int64   `tfsdk:"memory_mb"`
	PidsMax         types.Int64   `tfsdk:"pids_max"`
	Image           types.String  `tfsdk:"image"`
	Command         types.String  `tfsdk:"command"`
	Env             types.Map     `tfsdk:"env"`
	Port            types.Int64   `tfsdk:"port"`
	WarmPool        types.Object  `tfsdk:"warm_pool"`
	Phase           types.String  `tfsdk:"phase"`
	WarmCount       types.Int64   `tfsdk:"warm_count"`
}

// NewFunctionResource is the infra_function resource factory.
//
// function_instance, the pool slots this function's instances are tracked as,
// is intentionally not exposed as a resource: it is created and owned by the
// function controller, and managing it from Terraform too would race the
// controller's own pool reconciliation.
func NewFunctionResource() resource.Resource {
	return newGeneric(resourceDef[functionModel, infra.FunctionSpec, infra.FunctionStatus]{
		kind: infra.KindFunction,
		schema: schema.Schema{
			MarkdownDescription: "A FaaS function: the compute shape run for each invocation, plus a warm-pool " +
				"policy. Realized as ordinary compute instances that a controller creates and retires to the " +
				"policy, invoked synchronously over HTTP.",
			Attributes: map[string]schema.Attribute{
				"id":                idAttribute(),
				"name":              schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Display name."},
				"subnet_id":         schema.StringAttribute{Required: true, MarkdownDescription: "Subnet each instance attaches to."},
				"security_group_id": schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Security group applied to each instance."},
				"node_pool_id":      schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Node pool to schedule instances onto; empty schedules anywhere."},
				"cpu":               schema.Float64Attribute{Validators: []validator.Float64{runtimeFloatRange{min: 0.01, max: infra.MaxRuntimeCPU}}, Optional: true, Computed: true, MarkdownDescription: "CPU cores per instance; defaults to 1, range 0.01-64."},
				"memory_mb":         schema.Int64Attribute{Validators: []validator.Int64{runtimeIntRange{min: 1, max: infra.MaxRuntimeMemoryMB}}, Optional: true, Computed: true, MarkdownDescription: "Memory in MiB per instance; defaults to 256, maximum 262144."},
				"pids_max":          schema.Int64Attribute{Validators: []validator.Int64{runtimeIntRange{min: 1, max: infra.MaxRuntimePids}}, Optional: true, Computed: true, MarkdownDescription: "Maximum processes per instance; defaults to 256, maximum 4096."},
				"image":             schema.StringAttribute{Required: true, MarkdownDescription: "OCI image reference run on invocation."},
				"command":           schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Entrypoint command."},
				"env":               schema.MapAttribute{Optional: true, Computed: true, ElementType: types.StringType, MarkdownDescription: "Environment variables."},
				"port":              schema.Int64Attribute{Required: true, MarkdownDescription: "Port the runtime listens on inside the instance; invoke requests are forwarded to it."},
				"warm_pool": schema.SingleNestedAttribute{
					Optional:            true,
					Computed:            true,
					MarkdownDescription: "How many instances to keep pre-started and what to do when none are available.",
					Attributes: map[string]schema.Attribute{
						"min_warm":         schema.Int64Attribute{Validators: []validator.Int64{runtimeIntRange{min: 0, max: 256}}, Optional: true, Computed: true, MarkdownDescription: "Instances kept running regardless of idle time; defaults to 0, maximum 256."},
						"max_warm":         schema.Int64Attribute{Validators: []validator.Int64{runtimeIntRange{min: 1, max: 256}}, Optional: true, Computed: true, MarkdownDescription: "Cap on warm instances; defaults to max(32, min_warm), maximum 256."},
						"idle_ttl_seconds": schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "Seconds an instance above min_warm may sit idle before eviction; 0 evicts as soon as it is idle."},
						"allow_cold_start": schema.BoolAttribute{Optional: true, Computed: true, MarkdownDescription: "Permit creating a fresh instance on invoke when none are warm."},
					},
				},
				"phase":      phaseAttribute(),
				"warm_count": schema.Int64Attribute{Computed: true, MarkdownDescription: "Number of instances currently warm and backed by a ready compute."},
			},
		},
		toSpec:  functionToSpec,
		toModel: functionToModel,
		id:      func(m functionModel) string { return m.ID.ValueString() },
	})
}

func functionToSpec(ctx context.Context, m functionModel) (infra.FunctionSpec, diag.Diagnostics) {
	var diags diag.Diagnostics

	var env map[string]string
	if !m.Env.IsNull() && !m.Env.IsUnknown() {
		diags.Append(m.Env.ElementsAs(ctx, &env, false)...)
	}

	var warmPool infra.WarmPoolPolicy
	if !m.WarmPool.IsNull() && !m.WarmPool.IsUnknown() {
		var wp warmPoolModel
		diags.Append(m.WarmPool.As(ctx, &wp, basetypes.ObjectAsOptions{UnhandledUnknownAsEmpty: true})...)
		warmPool = infra.WarmPoolPolicy{
			MinWarm:        int(wp.MinWarm.ValueInt64()),
			MaxWarm:        int(wp.MaxWarm.ValueInt64()),
			IdleTTLSeconds: int(wp.IdleTTLSeconds.ValueInt64()),
			AllowColdStart: wp.AllowColdStart.ValueBool(),
		}
	}

	return infra.FunctionSpec{
		Name:            m.Name.ValueString(),
		SubnetID:        m.SubnetID.ValueString(),
		SecurityGroupID: m.SecurityGroupID.ValueString(),
		NodePoolID:      m.NodePoolID.ValueString(),
		CPU:             m.CPU.ValueFloat64(),
		MemoryMB:        int(m.MemoryMB.ValueInt64()),
		PidsMax:         int(m.PidsMax.ValueInt64()),
		Image:           m.Image.ValueString(),
		Command:         m.Command.ValueString(),
		Env:             env,
		Port:            int(m.Port.ValueInt64()),
		WarmPool:        warmPool,
	}, diags
}

func functionToModel(ctx context.Context, r *infra.Function) (functionModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	env, d := types.MapValueFrom(ctx, types.StringType, r.Spec.Env)
	diags.Append(d...)

	warmPool, d := types.ObjectValueFrom(ctx, warmPoolAttrTypes, warmPoolModel{
		MinWarm:        types.Int64Value(int64(r.Spec.WarmPool.MinWarm)),
		MaxWarm:        types.Int64Value(int64(r.Spec.WarmPool.MaxWarm)),
		IdleTTLSeconds: types.Int64Value(int64(r.Spec.WarmPool.IdleTTLSeconds)),
		AllowColdStart: types.BoolValue(r.Spec.WarmPool.AllowColdStart),
	})
	diags.Append(d...)

	return functionModel{
		ID:              types.StringValue(r.Metadata.UID),
		Name:            types.StringValue(r.Spec.Name),
		SubnetID:        types.StringValue(r.Spec.SubnetID),
		SecurityGroupID: types.StringValue(r.Spec.SecurityGroupID),
		NodePoolID:      types.StringValue(r.Spec.NodePoolID),
		CPU:             types.Float64Value(r.Spec.CPU),
		MemoryMB:        types.Int64Value(int64(r.Spec.MemoryMB)),
		PidsMax:         types.Int64Value(int64(r.Spec.PidsMax)),
		Image:           types.StringValue(r.Spec.Image),
		Command:         types.StringValue(r.Spec.Command),
		Env:             env,
		Port:            types.Int64Value(int64(r.Spec.Port)),
		WarmPool:        warmPool,
		Phase:           types.StringValue(string(r.Status.Phase)),
		WarmCount:       types.Int64Value(int64(r.Status.WarmCount)),
	}, diags
}
