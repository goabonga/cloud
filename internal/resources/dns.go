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

type dnsZoneModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	Domain     types.String `tfsdk:"domain"`
	Visibility types.String `tfsdk:"visibility"`
	VPCIDs     types.List   `tfsdk:"vpc_ids"`
	Phase      types.String `tfsdk:"phase"`
}

// NewDNSZoneResource is the infra_dns_zone resource factory.
func NewDNSZoneResource() resource.Resource {
	return newGeneric(resourceDef[dnsZoneModel, infra.DNSZoneSpec, infra.DNSZoneStatus]{
		kind: infra.KindDNSZone,
		schema: schema.Schema{
			MarkdownDescription: "A DNS zone.",
			Attributes: map[string]schema.Attribute{
				"id":         idAttribute(),
				"name":       schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Display name."},
				"domain":     schema.StringAttribute{Required: true, MarkdownDescription: "Zone domain, e.g. example.com."},
				"visibility": schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "public or private."},
				"vpc_ids":    schema.ListAttribute{Optional: true, Computed: true, ElementType: types.StringType, MarkdownDescription: "VPCs a private zone is attached to."},
				"phase":      phaseAttribute(),
			},
		},
		toSpec: func(ctx context.Context, m dnsZoneModel) (infra.DNSZoneSpec, diag.Diagnostics) {
			var diags diag.Diagnostics
			var vpcIDs []string
			if !m.VPCIDs.IsNull() && !m.VPCIDs.IsUnknown() {
				diags.Append(m.VPCIDs.ElementsAs(ctx, &vpcIDs, false)...)
			}
			return infra.DNSZoneSpec{
				Name:       m.Name.ValueString(),
				Domain:     m.Domain.ValueString(),
				Visibility: m.Visibility.ValueString(),
				VPCIDs:     vpcIDs,
			}, diags
		},
		toModel: func(ctx context.Context, r *infra.DNSZone) (dnsZoneModel, diag.Diagnostics) {
			vpcIDs, d := types.ListValueFrom(ctx, types.StringType, r.Spec.VPCIDs)
			return dnsZoneModel{
				ID:         types.StringValue(r.Metadata.UID),
				Name:       types.StringValue(r.Spec.Name),
				Domain:     types.StringValue(r.Spec.Domain),
				Visibility: types.StringValue(r.Spec.Visibility),
				VPCIDs:     vpcIDs,
				Phase:      types.StringValue(string(r.Status.Phase)),
			}, d
		},
		id: func(m dnsZoneModel) string { return m.ID.ValueString() },
	})
}

type dnsRecordModel struct {
	ID      types.String `tfsdk:"id"`
	ZoneID  types.String `tfsdk:"zone_id"`
	Name    types.String `tfsdk:"name"`
	Type    types.String `tfsdk:"type"`
	TTL     types.Int64  `tfsdk:"ttl"`
	Records types.List   `tfsdk:"records"`
	Phase   types.String `tfsdk:"phase"`
}

// NewDNSRecordResource is the infra_dns_record resource factory.
func NewDNSRecordResource() resource.Resource {
	return newGeneric(resourceDef[dnsRecordModel, infra.DNSRecordSpec, infra.DNSRecordStatus]{
		kind: infra.KindDNSRecord,
		schema: schema.Schema{
			MarkdownDescription: "A DNS record within a zone.",
			Attributes: map[string]schema.Attribute{
				"id":      idAttribute(),
				"zone_id": schema.StringAttribute{Required: true, MarkdownDescription: "Parent zone id."},
				"name":    schema.StringAttribute{Required: true, MarkdownDescription: "Record name."},
				"type":    schema.StringAttribute{Required: true, MarkdownDescription: "Record type, e.g. A, AAAA, CNAME, TXT."},
				"ttl":     schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "Time to live in seconds."},
				"records": schema.ListAttribute{Required: true, ElementType: types.StringType, MarkdownDescription: "Record values."},
				"phase":   phaseAttribute(),
			},
		},
		toSpec: func(ctx context.Context, m dnsRecordModel) (infra.DNSRecordSpec, diag.Diagnostics) {
			var diags diag.Diagnostics
			var records []string
			if !m.Records.IsNull() && !m.Records.IsUnknown() {
				diags.Append(m.Records.ElementsAs(ctx, &records, false)...)
			}
			return infra.DNSRecordSpec{
				ZoneID:  m.ZoneID.ValueString(),
				Name:    m.Name.ValueString(),
				Type:    m.Type.ValueString(),
				TTL:     int(m.TTL.ValueInt64()),
				Records: records,
			}, diags
		},
		toModel: func(ctx context.Context, r *infra.DNSRecord) (dnsRecordModel, diag.Diagnostics) {
			records, d := types.ListValueFrom(ctx, types.StringType, r.Spec.Records)
			return dnsRecordModel{
				ID:      types.StringValue(r.Metadata.UID),
				ZoneID:  types.StringValue(r.Spec.ZoneID),
				Name:    types.StringValue(r.Spec.Name),
				Type:    types.StringValue(r.Spec.Type),
				TTL:     types.Int64Value(int64(r.Spec.TTL)),
				Records: records,
				Phase:   types.StringValue(string(r.Status.Phase)),
			}, d
		},
		id: func(m dnsRecordModel) string { return m.ID.ValueString() },
	})
}
