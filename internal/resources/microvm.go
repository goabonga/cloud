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

type microvmModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	SubnetID         types.String `tfsdk:"subnet_id"`
	SecurityGroupID  types.String `tfsdk:"security_group_id"`
	Hostname         types.String `tfsdk:"hostname"`
	VCPUs            types.Int64  `tfsdk:"vcpus"`
	MemoryMB         types.Int64  `tfsdk:"memory_mb"`
	KernelPath       types.String `tfsdk:"kernel_path"`
	InitrdPath       types.String `tfsdk:"initrd_path"`
	CmdLine          types.String `tfsdk:"cmd_line"`
	Image            types.String `tfsdk:"image"`
	SSHAuthorizedKey types.String `tfsdk:"ssh_authorized_key"`
	UserData         types.String `tfsdk:"user_data"`
	IP               types.String `tfsdk:"ip"`
	Tap              types.String `tfsdk:"tap"`
	Pid              types.Int64  `tfsdk:"pid"`
	Ready            types.Bool   `tfsdk:"ready"`
	Phase            types.String `tfsdk:"phase"`
}

// NewMicroVMResource is the infra_microvm resource factory.
func NewMicroVMResource() resource.Resource {
	return newGeneric(resourceDef[microvmModel, infra.MicroVMSpec, infra.MicroVMStatus]{
		kind: infra.KindMicroVM,
		schema: schema.Schema{
			MarkdownDescription: "A micro-VM: a kernel and disk image booted under cloud-hypervisor, attached to a subnet.",
			Attributes: map[string]schema.Attribute{
				"id":                 idAttribute(),
				"name":               schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Display name."},
				"subnet_id":          schema.StringAttribute{Required: true, MarkdownDescription: "Subnet to attach to."},
				"security_group_id":  schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Security group to apply."},
				"hostname":           schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Instance hostname."},
				"vcpus":              schema.Int64Attribute{Required: true, MarkdownDescription: "Number of vCPUs."},
				"memory_mb":          schema.Int64Attribute{Required: true, MarkdownDescription: "Memory in MB."},
				"kernel_path":        schema.StringAttribute{Required: true, MarkdownDescription: "Absolute path to an uncompressed kernel on the agent's host."},
				"initrd_path":        schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Absolute path to an initramfs on the agent's host."},
				"cmd_line":           schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Extra kernel command-line arguments."},
				"image":              schema.StringAttribute{Required: true, MarkdownDescription: "Boot disk source: an http(s) URL or an absolute path on the agent's host."},
				"ssh_authorized_key": schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "SSH public key written to the guest's cloud-init config."},
				"user_data":          schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Raw cloud-init user-data, overriding the generated cloud-config."},
				"ip":                 schema.StringAttribute{Computed: true, MarkdownDescription: "Assigned IP."},
				"tap":                schema.StringAttribute{Computed: true, MarkdownDescription: "Host TAP device name."},
				"pid":                schema.Int64Attribute{Computed: true, MarkdownDescription: "cloud-hypervisor process id."},
				"ready":              schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the instance is ready."},
				"phase":              phaseAttribute(),
			},
		},
		toSpec:  microvmToSpec,
		toModel: microvmToModel,
		id:      func(m microvmModel) string { return m.ID.ValueString() },
	})
}

func microvmToSpec(_ context.Context, m microvmModel) (infra.MicroVMSpec, diag.Diagnostics) {
	var diags diag.Diagnostics
	return infra.MicroVMSpec{
		Name:             m.Name.ValueString(),
		SubnetID:         m.SubnetID.ValueString(),
		SecurityGroupID:  m.SecurityGroupID.ValueString(),
		Hostname:         m.Hostname.ValueString(),
		VCPUs:            int(m.VCPUs.ValueInt64()),
		MemoryMB:         int(m.MemoryMB.ValueInt64()),
		KernelPath:       m.KernelPath.ValueString(),
		InitrdPath:       m.InitrdPath.ValueString(),
		CmdLine:          m.CmdLine.ValueString(),
		Image:            m.Image.ValueString(),
		SSHAuthorizedKey: m.SSHAuthorizedKey.ValueString(),
		UserData:         m.UserData.ValueString(),
	}, diags
}

func microvmToModel(_ context.Context, r *infra.MicroVM) (microvmModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	return microvmModel{
		ID:               types.StringValue(r.Metadata.UID),
		Name:             types.StringValue(r.Spec.Name),
		SubnetID:         types.StringValue(r.Spec.SubnetID),
		SecurityGroupID:  types.StringValue(r.Spec.SecurityGroupID),
		Hostname:         types.StringValue(r.Spec.Hostname),
		VCPUs:            types.Int64Value(int64(r.Spec.VCPUs)),
		MemoryMB:         types.Int64Value(int64(r.Spec.MemoryMB)),
		KernelPath:       types.StringValue(r.Spec.KernelPath),
		InitrdPath:       types.StringValue(r.Spec.InitrdPath),
		CmdLine:          types.StringValue(r.Spec.CmdLine),
		Image:            types.StringValue(r.Spec.Image),
		SSHAuthorizedKey: types.StringValue(r.Spec.SSHAuthorizedKey),
		UserData:         types.StringValue(r.Spec.UserData),
		IP:               types.StringValue(r.Status.IP),
		Tap:              types.StringValue(r.Status.Tap),
		Pid:              types.Int64Value(int64(r.Status.Pid)),
		Ready:            types.BoolValue(r.Status.Ready),
		Phase:            types.StringValue(string(r.Status.Phase)),
	}, diags
}
