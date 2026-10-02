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

type kmsKeyringModel struct {
	ID    types.String `tfsdk:"id"`
	Name  types.String `tfsdk:"name"`
	Phase types.String `tfsdk:"phase"`
}

// NewKMSKeyringResource is the infra_kms_keyring resource factory.
func NewKMSKeyringResource() resource.Resource {
	return newGeneric(resourceDef[kmsKeyringModel, infra.KMSKeyringSpec, infra.KMSKeyringStatus]{
		kind: infra.KindKMSKeyring,
		schema: schema.Schema{
			MarkdownDescription: "A KMS keyring (a container for keys).",
			Attributes: map[string]schema.Attribute{
				"id":    idAttribute(),
				"name":  schema.StringAttribute{Required: true, MarkdownDescription: "Keyring name."},
				"phase": phaseAttribute(),
			},
		},
		toSpec: func(_ context.Context, m kmsKeyringModel) (infra.KMSKeyringSpec, diag.Diagnostics) {
			return infra.KMSKeyringSpec{Name: m.Name.ValueString()}, nil
		},
		toModel: func(_ context.Context, r *infra.KMSKeyring) (kmsKeyringModel, diag.Diagnostics) {
			return kmsKeyringModel{
				ID:    types.StringValue(r.Metadata.UID),
				Name:  types.StringValue(r.Spec.Name),
				Phase: types.StringValue(string(r.Status.Phase)),
			}, nil
		},
		id: func(m kmsKeyringModel) string { return m.ID.ValueString() },
	})
}

type kmsKeyModel struct {
	ID             types.String `tfsdk:"id"`
	KeyringID      types.String `tfsdk:"keyring_id"`
	Name           types.String `tfsdk:"name"`
	Purpose        types.String `tfsdk:"purpose"`
	Algorithm      types.String `tfsdk:"algorithm"`
	RotationPeriod types.String `tfsdk:"rotation_period"`
	ActiveVersion  types.Int64  `tfsdk:"active_version"`
	Phase          types.String `tfsdk:"phase"`
}

// NewKMSKeyResource is the infra_kms_key resource factory.
func NewKMSKeyResource() resource.Resource {
	return newGeneric(resourceDef[kmsKeyModel, infra.KMSKeySpec, infra.KMSKeyStatus]{
		kind: infra.KindKMSKey,
		schema: schema.Schema{
			MarkdownDescription: "A KMS key used to encrypt disks and secrets.",
			Attributes: map[string]schema.Attribute{
				"id":              idAttribute(),
				"keyring_id":      schema.StringAttribute{Required: true, MarkdownDescription: "Parent keyring id."},
				"name":            schema.StringAttribute{Required: true, MarkdownDescription: "Key name."},
				"purpose":         schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Key purpose."},
				"algorithm":       schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Algorithm, e.g. AES-256."},
				"rotation_period": schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Rotation period, e.g. 90d."},
				"active_version":  schema.Int64Attribute{Computed: true, MarkdownDescription: "Active key version."},
				"phase":           phaseAttribute(),
			},
		},
		toSpec: func(_ context.Context, m kmsKeyModel) (infra.KMSKeySpec, diag.Diagnostics) {
			return infra.KMSKeySpec{
				KeyringID:      m.KeyringID.ValueString(),
				Name:           m.Name.ValueString(),
				Purpose:        m.Purpose.ValueString(),
				Algorithm:      m.Algorithm.ValueString(),
				RotationPeriod: m.RotationPeriod.ValueString(),
			}, nil
		},
		toModel: func(_ context.Context, r *infra.KMSKey) (kmsKeyModel, diag.Diagnostics) {
			return kmsKeyModel{
				ID:             types.StringValue(r.Metadata.UID),
				KeyringID:      types.StringValue(r.Spec.KeyringID),
				Name:           types.StringValue(r.Spec.Name),
				Purpose:        types.StringValue(r.Spec.Purpose),
				Algorithm:      types.StringValue(r.Spec.Algorithm),
				RotationPeriod: types.StringValue(r.Spec.RotationPeriod),
				ActiveVersion:  types.Int64Value(int64(r.Status.ActiveVersion)),
				Phase:          types.StringValue(string(r.Status.Phase)),
			}, nil
		},
		id: func(m kmsKeyModel) string { return m.ID.ValueString() },
	})
}
