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

type diskModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	SizeMB    types.Int64  `tfsdk:"size_mb"`
	KMSKeyID  types.String `tfsdk:"kms_key_id"`
	Encrypted types.Bool   `tfsdk:"encrypted"`
	Path      types.String `tfsdk:"path"`
	Phase     types.String `tfsdk:"phase"`
}

// NewDiskResource is the infra_disk resource factory.
func NewDiskResource() resource.Resource {
	return newGeneric(resourceDef[diskModel, infra.DiskSpec, infra.DiskStatus]{
		kind: infra.KindDisk,
		schema: schema.Schema{
			MarkdownDescription: "A persistent disk; set kms_key_id to encrypt it at rest.",
			Attributes: map[string]schema.Attribute{
				"id":   idAttribute(),
				"name": schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Display name."},
				// The agent creates a disk's image once: a new size or key
				// only takes effect on a new disk.
				"size_mb":    schema.Int64Attribute{Required: true, PlanModifiers: replaceInt64(), MarkdownDescription: "Disk size in MB; changing it replaces the disk."},
				"kms_key_id": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: replaceString(), MarkdownDescription: "KMS key for encryption; changing it replaces the disk."},
				"encrypted":  schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the disk is encrypted."},
				"path":       schema.StringAttribute{Computed: true, MarkdownDescription: "Device path assigned by the agent."},
				"phase":      phaseAttribute(),
			},
		},
		toSpec: func(_ context.Context, m diskModel) (infra.DiskSpec, diag.Diagnostics) {
			return infra.DiskSpec{Name: m.Name.ValueString(), SizeMB: int(m.SizeMB.ValueInt64()), KMSKeyID: m.KMSKeyID.ValueString()}, nil
		},
		toModel: func(_ context.Context, r *infra.Disk) (diskModel, diag.Diagnostics) {
			return diskModel{
				ID:        types.StringValue(r.Metadata.UID),
				Name:      types.StringValue(r.Spec.Name),
				SizeMB:    types.Int64Value(int64(r.Spec.SizeMB)),
				KMSKeyID:  types.StringValue(r.Spec.KMSKeyID),
				Encrypted: types.BoolValue(r.Status.Encrypted),
				Path:      types.StringValue(r.Status.Path),
				Phase:     types.StringValue(string(r.Status.Phase)),
			}, nil
		},
		id: func(m diskModel) string { return m.ID.ValueString() },
	})
}

type diskFileModel struct {
	ID      types.String `tfsdk:"id"`
	DiskID  types.String `tfsdk:"disk_id"`
	Path    types.String `tfsdk:"path"`
	Content types.String `tfsdk:"content"`
	Mode    types.String `tfsdk:"mode"`
	SSLCert types.String `tfsdk:"ssl_cert_id"`
	SSLPart types.String `tfsdk:"ssl_part"`
	Phase   types.String `tfsdk:"phase"`
}

// NewDiskFileResource is the infra_disk_file resource factory.
func NewDiskFileResource() resource.Resource {
	return newGeneric(resourceDef[diskFileModel, infra.DiskFileSpec, infra.DiskFileStatus]{
		kind: infra.KindDiskFile,
		schema: schema.Schema{
			MarkdownDescription: "A file injected into a disk's filesystem: either `content`, or one part of an " +
				"`infra_ssl_cert` (`ssl_cert_id` and `ssl_part`), rendered by the agent so the private key never " +
				"passes through Terraform.",
			Attributes: map[string]schema.Attribute{
				"id":          idAttribute(),
				"disk_id":     schema.StringAttribute{Required: true, MarkdownDescription: "Target disk id."},
				"path":        schema.StringAttribute{Required: true, MarkdownDescription: "Path within the disk."},
				"content":     schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "File content; exclusive with `ssl_cert_id`."},
				"mode":        schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "File mode, e.g. 0644; 0600 by default for a private key."},
				"ssl_cert_id": schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Certificate whose part the file holds."},
				"ssl_part": schema.StringAttribute{Optional: true, Computed: true,
					MarkdownDescription: "Part of the certificate: `certificate`, `chain` (certificate then CA) or `private_key`."},
				"phase": phaseAttribute(),
			},
		},
		toSpec: func(_ context.Context, m diskFileModel) (infra.DiskFileSpec, diag.Diagnostics) {
			return infra.DiskFileSpec{
				DiskID:    m.DiskID.ValueString(),
				Path:      m.Path.ValueString(),
				Content:   m.Content.ValueString(),
				Mode:      m.Mode.ValueString(),
				SSLCertID: m.SSLCert.ValueString(),
				SSLPart:   m.SSLPart.ValueString(),
			}, nil
		},
		toModel: func(_ context.Context, r *infra.DiskFile) (diskFileModel, diag.Diagnostics) {
			return diskFileModel{
				ID:      types.StringValue(r.Metadata.UID),
				DiskID:  types.StringValue(r.Spec.DiskID),
				Path:    types.StringValue(r.Spec.Path),
				Content: types.StringValue(r.Spec.Content),
				Mode:    types.StringValue(r.Spec.Mode),
				SSLCert: types.StringValue(r.Spec.SSLCertID),
				SSLPart: types.StringValue(r.Spec.SSLPart),
				Phase:   types.StringValue(string(r.Status.Phase)),
			}, nil
		},
		id: func(m diskFileModel) string { return m.ID.ValueString() },
	})
}
