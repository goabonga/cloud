// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/float64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

type computeDiskRefModel struct {
	DiskID    types.String `tfsdk:"disk_id"`
	MountPath types.String `tfsdk:"mount_path"`
	ReadOnly  types.Bool   `tfsdk:"read_only"`
}

var diskRefObjectType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"disk_id":    types.StringType,
	"mount_path": types.StringType,
	"read_only":  types.BoolType,
}}

type computeModel struct {
	ID              types.String  `tfsdk:"id"`
	Name            types.String  `tfsdk:"name"`
	SubnetID        types.String  `tfsdk:"subnet_id"`
	SecurityGroupID types.String  `tfsdk:"security_group_id"`
	NodePoolID      types.String  `tfsdk:"node_pool_id"`
	Hostname        types.String  `tfsdk:"hostname"`
	CPU             types.Float64 `tfsdk:"cpu"`
	MemoryMB        types.Int64   `tfsdk:"memory_mb"`
	PidsMax         types.Int64   `tfsdk:"pids_max"`
	Image           types.String  `tfsdk:"image"`
	Command         types.String  `tfsdk:"command"`
	Env             types.Map     `tfsdk:"env"`
	Ports           types.List    `tfsdk:"ports"`
	Disks           types.List    `tfsdk:"disks"`
	Privileged      types.Bool    `tfsdk:"privileged"`
	IP              types.String  `tfsdk:"ip"`
	Ready           types.Bool    `tfsdk:"ready"`
	Phase           types.String  `tfsdk:"phase"`
}

// NewComputeResource is the infra_compute resource factory.
func NewComputeResource() resource.Resource {
	return newGeneric(resourceDef[computeModel, infra.ComputeSpec, infra.ComputeStatus]{
		kind: infra.KindCompute,
		schema: schema.Schema{
			MarkdownDescription: "A compute instance: an OCI image run in a network namespace with attached disks. " +
				"The agent realizes an instance once, so changing anything but its name replaces it.",
			Attributes: map[string]schema.Attribute{
				"id":                idAttribute(),
				"name":              schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Display name."},
				"subnet_id":         schema.StringAttribute{Required: true, PlanModifiers: replaceString(), MarkdownDescription: "Subnet to attach to."},
				"security_group_id": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: replaceString(), MarkdownDescription: "Security group to apply."},
				"node_pool_id":      schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: replaceString(), MarkdownDescription: "Node pool to schedule onto; empty schedules anywhere."},
				"hostname":          schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: replaceString(), MarkdownDescription: "Instance hostname."},
				"cpu":               schema.Float64Attribute{Optional: true, Computed: true, PlanModifiers: replaceFloat64(), MarkdownDescription: "CPU cores."},
				"memory_mb":         schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: replaceInt64(), MarkdownDescription: "Memory in MB."},
				"pids_max":          schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: replaceInt64(), MarkdownDescription: "Maximum processes."},
				"image":             schema.StringAttribute{Required: true, PlanModifiers: replaceString(), MarkdownDescription: "OCI image reference."},
				"command":           schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: replaceString(), MarkdownDescription: "Entrypoint command."},
				"env":               schema.MapAttribute{Optional: true, Computed: true, PlanModifiers: replaceMap(), ElementType: types.StringType, MarkdownDescription: "Environment variables."},
				"ports":             schema.ListAttribute{Optional: true, Computed: true, PlanModifiers: replaceList(), ElementType: types.StringType, MarkdownDescription: "Exposed ports."},
				"privileged":        schema.BoolAttribute{Optional: true, Computed: true, PlanModifiers: replaceBool(), MarkdownDescription: "Run privileged."},
				"disks": schema.ListNestedAttribute{
					Optional:            true,
					Computed:            true,
					PlanModifiers:       replaceList(),
					MarkdownDescription: "Disks to attach.",
					NestedObject: schema.NestedAttributeObject{
						Attributes: map[string]schema.Attribute{
							"disk_id":    schema.StringAttribute{Required: true, MarkdownDescription: "Disk id."},
							"mount_path": schema.StringAttribute{Required: true, MarkdownDescription: "Mount path."},
							"read_only":  schema.BoolAttribute{Optional: true, Computed: true, MarkdownDescription: "Mount read-only."},
						},
					},
				},
				"ip":    schema.StringAttribute{Computed: true, MarkdownDescription: "Assigned IP."},
				"ready": schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the instance is ready."},
				"phase": phaseAttribute(),
			},
		},
		toSpec:  computeToSpec,
		toModel: computeToModel,
		id:      func(m computeModel) string { return m.ID.ValueString() },
	})
}

func computeToSpec(ctx context.Context, m computeModel) (infra.ComputeSpec, diag.Diagnostics) {
	var diags diag.Diagnostics

	var env map[string]string
	if !m.Env.IsNull() && !m.Env.IsUnknown() {
		diags.Append(m.Env.ElementsAs(ctx, &env, false)...)
	}
	var ports []string
	if !m.Ports.IsNull() && !m.Ports.IsUnknown() {
		diags.Append(m.Ports.ElementsAs(ctx, &ports, false)...)
	}
	var disks []infra.ComputeDiskRef
	if !m.Disks.IsNull() && !m.Disks.IsUnknown() {
		var refs []computeDiskRefModel
		diags.Append(m.Disks.ElementsAs(ctx, &refs, false)...)
		for _, r := range refs {
			disks = append(disks, infra.ComputeDiskRef{
				DiskID:    r.DiskID.ValueString(),
				MountPath: r.MountPath.ValueString(),
				ReadOnly:  r.ReadOnly.ValueBool(),
			})
		}
	}

	return infra.ComputeSpec{
		Name:            m.Name.ValueString(),
		SubnetID:        m.SubnetID.ValueString(),
		SecurityGroupID: m.SecurityGroupID.ValueString(),
		NodePoolID:      m.NodePoolID.ValueString(),
		Hostname:        m.Hostname.ValueString(),
		CPU:             m.CPU.ValueFloat64(),
		MemoryMB:        int(m.MemoryMB.ValueInt64()),
		PidsMax:         int(m.PidsMax.ValueInt64()),
		Image:           m.Image.ValueString(),
		Command:         m.Command.ValueString(),
		Env:             env,
		Ports:           ports,
		Disks:           disks,
		Privileged:      m.Privileged.ValueBool(),
	}, diags
}

func computeToModel(ctx context.Context, r *infra.Compute) (computeModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	env, d := types.MapValueFrom(ctx, types.StringType, r.Spec.Env)
	diags.Append(d...)
	ports, d := types.ListValueFrom(ctx, types.StringType, r.Spec.Ports)
	diags.Append(d...)

	refs := make([]computeDiskRefModel, 0, len(r.Spec.Disks))
	for _, dref := range r.Spec.Disks {
		refs = append(refs, computeDiskRefModel{
			DiskID:    types.StringValue(dref.DiskID),
			MountPath: types.StringValue(dref.MountPath),
			ReadOnly:  types.BoolValue(dref.ReadOnly),
		})
	}
	disks, d := types.ListValueFrom(ctx, diskRefObjectType, refs)
	diags.Append(d...)

	return computeModel{
		ID:              types.StringValue(r.Metadata.UID),
		Name:            types.StringValue(r.Spec.Name),
		SubnetID:        types.StringValue(r.Spec.SubnetID),
		SecurityGroupID: types.StringValue(r.Spec.SecurityGroupID),
		NodePoolID:      types.StringValue(r.Spec.NodePoolID),
		Hostname:        types.StringValue(r.Spec.Hostname),
		CPU:             types.Float64Value(r.Spec.CPU),
		MemoryMB:        types.Int64Value(int64(r.Spec.MemoryMB)),
		PidsMax:         types.Int64Value(int64(r.Spec.PidsMax)),
		Image:           types.StringValue(r.Spec.Image),
		Command:         types.StringValue(r.Spec.Command),
		Env:             env,
		Ports:           ports,
		Disks:           disks,
		Privileged:      types.BoolValue(r.Spec.Privileged),
		IP:              types.StringValue(r.Status.IP),
		Ready:           types.BoolValue(r.Status.Ready),
		Phase:           types.StringValue(string(r.Status.Phase)),
	}, diags
}

// The agent realizes an instance once, when it creates its namespace, and
// leaves it alone afterwards: a change to its spec only takes effect on a new
// instance. An attribute left out of the configuration keeps its state value,
// so a change elsewhere does not read as a change of it.

func replaceString() []planmodifier.String {
	return []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()}
}

func replaceFloat64() []planmodifier.Float64 {
	return []planmodifier.Float64{float64planmodifier.UseStateForUnknown(), float64planmodifier.RequiresReplace()}
}

func replaceInt64() []planmodifier.Int64 {
	return []planmodifier.Int64{int64planmodifier.UseStateForUnknown(), int64planmodifier.RequiresReplace()}
}

func replaceBool() []planmodifier.Bool {
	return []planmodifier.Bool{boolplanmodifier.UseStateForUnknown(), boolplanmodifier.RequiresReplace()}
}

func replaceMap() []planmodifier.Map {
	return []planmodifier.Map{mapplanmodifier.UseStateForUnknown(), mapplanmodifier.RequiresReplace()}
}

func replaceList() []planmodifier.List {
	return []planmodifier.List{listplanmodifier.UseStateForUnknown(), listplanmodifier.RequiresReplace()}
}
