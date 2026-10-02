// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

type ipAddressModel struct {
	ID        types.String `tfsdk:"id"`
	Type      types.String `tfsdk:"type"`
	SubnetID  types.String `tfsdk:"subnet_id"`
	VPCID     types.String `tfsdk:"vpc_id"`
	ComputeID types.String `tfsdk:"compute_id"`
	Address   types.String `tfsdk:"address"`
	Phase     types.String `tfsdk:"phase"`
}

// NewIPAddressResource is the infra_ip_address resource factory.
func NewIPAddressResource() resource.Resource {
	return newGeneric(resourceDef[ipAddressModel, infra.IPAddressSpec, infra.IPAddressStatus]{
		kind: infra.KindIPAddress,
		schema: schema.Schema{
			MarkdownDescription: "A reserved IP address.",
			Attributes: map[string]schema.Attribute{
				"id":         idAttribute(),
				"type":       schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "private or public."},
				"subnet_id":  schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Subnet to allocate from."},
				"vpc_id":     schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "VPC scope."},
				"compute_id": schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Compute to bind to."},
				"address":    schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Requested or resolved address."},
				"phase":      phaseAttribute(),
			},
		},
		toSpec: func(_ context.Context, m ipAddressModel) (infra.IPAddressSpec, diag.Diagnostics) {
			return infra.IPAddressSpec{
				Type:      m.Type.ValueString(),
				SubnetID:  m.SubnetID.ValueString(),
				VPCID:     m.VPCID.ValueString(),
				ComputeID: m.ComputeID.ValueString(),
				Address:   m.Address.ValueString(),
			}, nil
		},
		toModel: func(_ context.Context, r *infra.IPAddress) (ipAddressModel, diag.Diagnostics) {
			address := r.Status.Address
			if address == "" {
				address = r.Spec.Address
			}
			return ipAddressModel{
				ID:        types.StringValue(r.Metadata.UID),
				Type:      types.StringValue(r.Spec.Type),
				SubnetID:  types.StringValue(r.Spec.SubnetID),
				VPCID:     types.StringValue(r.Spec.VPCID),
				ComputeID: types.StringValue(r.Spec.ComputeID),
				Address:   types.StringValue(address),
				Phase:     types.StringValue(string(r.Status.Phase)),
			}, nil
		},
		id: func(m ipAddressModel) string { return m.ID.ValueString() },
	})
}

type igwModel struct {
	ID                 types.String `tfsdk:"id"`
	VPCID              types.String `tfsdk:"vpc_id"`
	EgressProxy        types.Object `tfsdk:"egress_proxy"`
	EgressProxyAddress types.String `tfsdk:"egress_proxy_address"`
	HostIface          types.String `tfsdk:"host_iface"`
	Bridge             types.String `tfsdk:"bridge"`
	Phase              types.String `tfsdk:"phase"`
}

type egressProxyModel struct {
	Enabled          types.Bool `tfsdk:"enabled"`
	AllowedDomains   types.List `tfsdk:"allowed_domains"`
	AllowedAddresses types.List `tfsdk:"allowed_addresses"`
}

type egressAddressModel struct {
	CIDR     types.String `tfsdk:"cidr"`
	Protocol types.String `tfsdk:"protocol"`
	Port     types.Int64  `tfsdk:"port"`
}

var egressAddressType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"cidr":     types.StringType,
	"protocol": types.StringType,
	"port":     types.Int64Type,
}}

var egressProxyAttrTypes = map[string]attr.Type{
	"enabled":           types.BoolType,
	"allowed_domains":   types.ListType{ElemType: types.StringType},
	"allowed_addresses": types.ListType{ElemType: egressAddressType},
}

// The egress proxy's optional fields read back as null when unset, so a
// configuration leaving them out plans no change.
func nullableString(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func nullableInt(i int) types.Int64 {
	if i == 0 {
		return types.Int64Null()
	}
	return types.Int64Value(int64(i))
}

func egressProxyToSpec(ctx context.Context, obj types.Object) (*infra.EgressProxySpec, diag.Diagnostics) {
	var diags diag.Diagnostics
	if obj.IsNull() || obj.IsUnknown() {
		return nil, diags
	}
	var m egressProxyModel
	diags.Append(obj.As(ctx, &m, basetypes.ObjectAsOptions{UnhandledUnknownAsEmpty: true})...)
	spec := &infra.EgressProxySpec{Enabled: m.Enabled.ValueBool()}
	if !m.AllowedDomains.IsNull() && !m.AllowedDomains.IsUnknown() {
		diags.Append(m.AllowedDomains.ElementsAs(ctx, &spec.AllowedDomains, false)...)
	}
	if !m.AllowedAddresses.IsNull() && !m.AllowedAddresses.IsUnknown() {
		var addrs []egressAddressModel
		diags.Append(m.AllowedAddresses.ElementsAs(ctx, &addrs, false)...)
		for _, a := range addrs {
			spec.AllowedAddresses = append(spec.AllowedAddresses, infra.EgressAddress{
				CIDR: a.CIDR.ValueString(), Protocol: a.Protocol.ValueString(), Port: int(a.Port.ValueInt64()),
			})
		}
	}
	return spec, diags
}

func egressProxyToModel(ctx context.Context, spec *infra.EgressProxySpec) (types.Object, diag.Diagnostics) {
	if spec == nil {
		return types.ObjectNull(egressProxyAttrTypes), nil
	}
	var diags diag.Diagnostics
	domains := types.ListNull(types.StringType)
	if len(spec.AllowedDomains) > 0 {
		var d diag.Diagnostics
		domains, d = types.ListValueFrom(ctx, types.StringType, spec.AllowedDomains)
		diags.Append(d...)
	}
	addrs := types.ListNull(egressAddressType)
	if len(spec.AllowedAddresses) > 0 {
		models := make([]egressAddressModel, 0, len(spec.AllowedAddresses))
		for _, a := range spec.AllowedAddresses {
			models = append(models, egressAddressModel{CIDR: types.StringValue(a.CIDR), Protocol: nullableString(a.Protocol), Port: nullableInt(a.Port)})
		}
		var d diag.Diagnostics
		addrs, d = types.ListValueFrom(ctx, egressAddressType, models)
		diags.Append(d...)
	}
	obj, d := types.ObjectValueFrom(ctx, egressProxyAttrTypes, egressProxyModel{
		Enabled: types.BoolValue(spec.Enabled), AllowedDomains: domains, AllowedAddresses: addrs,
	})
	diags.Append(d...)
	return obj, diags
}

// NewIGWResource is the infra_igw resource factory.
func NewIGWResource() resource.Resource {
	return newGeneric(resourceDef[igwModel, infra.IGWSpec, infra.IGWStatus]{
		kind: infra.KindIGW,
		schema: schema.Schema{
			MarkdownDescription: "An internet gateway for a VPC.",
			Attributes: map[string]schema.Attribute{
				"id":     idAttribute(),
				"vpc_id": schema.StringAttribute{Required: true, MarkdownDescription: "Parent VPC id."},
				"egress_proxy": schema.SingleNestedAttribute{
					Optional: true,
					MarkdownDescription: "Filters the VPC's egress: HTTP and HTTPS to public addresses go through a proxy, " +
						"transparently or explicitly on `egress_proxy_address`:3128, which lets out only `allowed_domains`; " +
						"any other new connection out of the gateway is refused unless its destination is in `allowed_addresses`.",
					Attributes: map[string]schema.Attribute{
						"enabled": schema.BoolAttribute{Required: true, MarkdownDescription: "Whether the proxy filters the egress."},
						"allowed_domains": schema.ListAttribute{Optional: true, ElementType: types.StringType,
							MarkdownDescription: "Names let through: `example.com`, or `*.example.com` for its subdomains."},
						"allowed_addresses": schema.ListNestedAttribute{
							Optional:            true,
							MarkdownDescription: "Destinations reachable directly, and through the proxy by address.",
							NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
								"cidr":     schema.StringAttribute{Required: true, MarkdownDescription: "An address or a CIDR block."},
								"protocol": schema.StringAttribute{Optional: true, MarkdownDescription: "tcp, udp or any (default)."},
								"port":     schema.Int64Attribute{Optional: true, MarkdownDescription: "With tcp or udp, the one port allowed; all when omitted."},
							}},
						},
					},
				},
				"egress_proxy_address": schema.StringAttribute{Computed: true, MarkdownDescription: "Where the egress proxy listens, explicitly on port 3128."},
				"host_iface":           schema.StringAttribute{Computed: true, MarkdownDescription: "Host interface used for egress."},
				"bridge":               schema.StringAttribute{Computed: true, MarkdownDescription: "Bridge backing the gateway."},
				"phase":                phaseAttribute(),
			},
		},
		toSpec: func(ctx context.Context, m igwModel) (infra.IGWSpec, diag.Diagnostics) {
			proxy, diags := egressProxyToSpec(ctx, m.EgressProxy)
			return infra.IGWSpec{VPCID: m.VPCID.ValueString(), EgressProxy: proxy}, diags
		},
		toModel: func(ctx context.Context, r *infra.IGW) (igwModel, diag.Diagnostics) {
			proxy, diags := egressProxyToModel(ctx, r.Spec.EgressProxy)
			addr := ""
			if r.Status.EgressProxy != nil {
				addr = r.Status.EgressProxy.Address
			}
			return igwModel{
				ID:                 types.StringValue(r.Metadata.UID),
				VPCID:              types.StringValue(r.Spec.VPCID),
				EgressProxy:        proxy,
				EgressProxyAddress: types.StringValue(addr),
				HostIface:          types.StringValue(r.Status.HostIface),
				Bridge:             types.StringValue(r.Status.Bridge),
				Phase:              types.StringValue(string(r.Status.Phase)),
			}, diags
		},
		id: func(m igwModel) string { return m.ID.ValueString() },
	})
}

type routeModel struct {
	ID          types.String `tfsdk:"id"`
	VPCID       types.String `tfsdk:"vpc_id"`
	SubnetID    types.String `tfsdk:"subnet_id"`
	Destination types.String `tfsdk:"destination"`
	Gateway     types.String `tfsdk:"gateway"`
	Phase       types.String `tfsdk:"phase"`
}

// NewRouteResource is the infra_route resource factory.
func NewRouteResource() resource.Resource {
	return newGeneric(resourceDef[routeModel, infra.RouteSpec, infra.RouteStatus]{
		kind: infra.KindRoute,
		schema: schema.Schema{
			MarkdownDescription: "A static route within a VPC.",
			Attributes: map[string]schema.Attribute{
				"id":          idAttribute(),
				"vpc_id":      schema.StringAttribute{Required: true, MarkdownDescription: "Parent VPC id."},
				"subnet_id":   schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "Subnet scope (optional)."},
				"destination": schema.StringAttribute{Required: true, MarkdownDescription: "Destination CIDR."},
				"gateway":     schema.StringAttribute{Required: true, MarkdownDescription: "Target (igw id, local, or peer)."},
				"phase":       phaseAttribute(),
			},
		},
		toSpec: func(_ context.Context, m routeModel) (infra.RouteSpec, diag.Diagnostics) {
			return infra.RouteSpec{
				VPCID:       m.VPCID.ValueString(),
				SubnetID:    m.SubnetID.ValueString(),
				Destination: m.Destination.ValueString(),
				Gateway:     m.Gateway.ValueString(),
			}, nil
		},
		toModel: func(_ context.Context, r *infra.Route) (routeModel, diag.Diagnostics) {
			return routeModel{
				ID:          types.StringValue(r.Metadata.UID),
				VPCID:       types.StringValue(r.Spec.VPCID),
				SubnetID:    types.StringValue(r.Spec.SubnetID),
				Destination: types.StringValue(r.Spec.Destination),
				Gateway:     types.StringValue(r.Spec.Gateway),
				Phase:       types.StringValue(string(r.Status.Phase)),
			}, nil
		},
		id: func(m routeModel) string { return m.ID.ValueString() },
	})
}
