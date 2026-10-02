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

type sslCAModel struct {
	ID           types.String `tfsdk:"id"`
	CommonName   types.String `tfsdk:"common_name"`
	Organization types.String `tfsdk:"organization"`
	ValidDays    types.Int64  `tfsdk:"valid_days"`
	VPCIDs       types.List   `tfsdk:"vpc_ids"`
	CertPEM      types.String `tfsdk:"cert_pem"`
	Phase        types.String `tfsdk:"phase"`
}

// NewSSLCAResource is the infra_ssl_ca resource factory.
func NewSSLCAResource() resource.Resource {
	return newGeneric(resourceDef[sslCAModel, infra.SSLCASpec, infra.SSLCAStatus]{
		kind: infra.KindSSLCA,
		schema: schema.Schema{
			MarkdownDescription: "A certificate authority. The instances of the VPCs it names trust it. " +
				"The platform's global public root, trusted everywhere, has the id `public-root`.",
			Attributes: map[string]schema.Attribute{
				"id":           idAttribute(),
				"common_name":  schema.StringAttribute{Required: true, MarkdownDescription: "Subject common name."},
				"organization": schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Subject organization."},
				"valid_days":   schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "Lifetime in days; the server default when omitted."},
				"vpc_ids":      schema.ListAttribute{Optional: true, Computed: true, ElementType: types.StringType, MarkdownDescription: "VPCs whose instances trust the CA."},
				"cert_pem":     schema.StringAttribute{Computed: true, MarkdownDescription: "The CA certificate, PEM."},
				"phase":        phaseAttribute(),
			},
		},
		toSpec: func(ctx context.Context, m sslCAModel) (infra.SSLCASpec, diag.Diagnostics) {
			var diags diag.Diagnostics
			var vpcIDs []string
			if !m.VPCIDs.IsNull() && !m.VPCIDs.IsUnknown() {
				diags.Append(m.VPCIDs.ElementsAs(ctx, &vpcIDs, false)...)
			}
			return infra.SSLCASpec{
				CommonName:   m.CommonName.ValueString(),
				Organization: m.Organization.ValueString(),
				ValidDays:    int(m.ValidDays.ValueInt64()),
				VPCIDs:       vpcIDs,
			}, diags
		},
		toModel: func(ctx context.Context, r *infra.SSLCA) (sslCAModel, diag.Diagnostics) {
			vpcIDs, d := types.ListValueFrom(ctx, types.StringType, r.Spec.VPCIDs)
			return sslCAModel{
				ID:           types.StringValue(r.Metadata.UID),
				CommonName:   types.StringValue(r.Spec.CommonName),
				Organization: types.StringValue(r.Spec.Organization),
				ValidDays:    types.Int64Value(int64(r.Spec.ValidDays)),
				VPCIDs:       vpcIDs,
				CertPEM:      types.StringValue(string(r.Status.CertPEM)),
				Phase:        types.StringValue(string(r.Status.Phase)),
			}, d
		},
		id: func(m sslCAModel) string { return m.ID.ValueString() },
	})
}

type sslCertModel struct {
	ID          types.String `tfsdk:"id"`
	CAID        types.String `tfsdk:"ca_id"`
	CommonName  types.String `tfsdk:"common_name"`
	DNSNames    types.List   `tfsdk:"dns_names"`
	IPAddresses types.List   `tfsdk:"ip_addresses"`
	ValidDays   types.Int64  `tfsdk:"valid_days"`
	CertPEM     types.String `tfsdk:"cert_pem"`
	Phase       types.String `tfsdk:"phase"`
}

// NewSSLCertResource is the infra_ssl_cert resource factory.
func NewSSLCertResource() resource.Resource {
	return newGeneric(resourceDef[sslCertModel, infra.SSLCertSpec, infra.SSLCertStatus]{
		kind: infra.KindSSLCert,
		schema: schema.Schema{
			MarkdownDescription: "A leaf certificate signed by a CA. Its private key stays encrypted on the platform; " +
				"an `infra_disk_file` with `ssl_cert_id` puts it on an instance's disk.",
			Attributes: map[string]schema.Attribute{
				"id":           idAttribute(),
				"ca_id":        schema.StringAttribute{Required: true, MarkdownDescription: "Signing CA id; `public-root` for public names."},
				"common_name":  schema.StringAttribute{Required: true, MarkdownDescription: "Subject common name."},
				"dns_names":    schema.ListAttribute{Optional: true, Computed: true, ElementType: types.StringType, MarkdownDescription: "DNS subject alternative names."},
				"ip_addresses": schema.ListAttribute{Optional: true, Computed: true, ElementType: types.StringType, MarkdownDescription: "IP subject alternative names."},
				"valid_days":   schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "Lifetime in days; the server default when omitted."},
				"cert_pem":     schema.StringAttribute{Computed: true, MarkdownDescription: "The certificate, PEM."},
				"phase":        phaseAttribute(),
			},
		},
		toSpec: func(ctx context.Context, m sslCertModel) (infra.SSLCertSpec, diag.Diagnostics) {
			var diags diag.Diagnostics
			var dnsNames, ips []string
			if !m.DNSNames.IsNull() && !m.DNSNames.IsUnknown() {
				diags.Append(m.DNSNames.ElementsAs(ctx, &dnsNames, false)...)
			}
			if !m.IPAddresses.IsNull() && !m.IPAddresses.IsUnknown() {
				diags.Append(m.IPAddresses.ElementsAs(ctx, &ips, false)...)
			}
			return infra.SSLCertSpec{
				CAID:        m.CAID.ValueString(),
				CommonName:  m.CommonName.ValueString(),
				DNSNames:    dnsNames,
				IPAddresses: ips,
				ValidDays:   int(m.ValidDays.ValueInt64()),
			}, diags
		},
		toModel: func(ctx context.Context, r *infra.SSLCert) (sslCertModel, diag.Diagnostics) {
			var diags diag.Diagnostics
			dnsNames, d := types.ListValueFrom(ctx, types.StringType, r.Spec.DNSNames)
			diags.Append(d...)
			ips, d := types.ListValueFrom(ctx, types.StringType, r.Spec.IPAddresses)
			diags.Append(d...)
			return sslCertModel{
				ID:          types.StringValue(r.Metadata.UID),
				CAID:        types.StringValue(r.Spec.CAID),
				CommonName:  types.StringValue(r.Spec.CommonName),
				DNSNames:    dnsNames,
				IPAddresses: ips,
				ValidDays:   types.Int64Value(int64(r.Spec.ValidDays)),
				CertPEM:     types.StringValue(string(r.Status.CertPEM)),
				Phase:       types.StringValue(string(r.Status.Phase)),
			}, diags
		},
		id: func(m sslCertModel) string { return m.ID.ValueString() },
	})
}
