// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

func TestNewResourceMetadataAndSchema(t *testing.T) {
	t.Parallel()

	cases := []struct {
		factory  func() resource.Resource
		wantType string
		required []string
	}{
		{NewDNSZoneResource, "infra_dns_zone", []string{"domain"}},
		{NewDNSRecordResource, "infra_dns_record", []string{"zone_id", "name", "type", "records"}},
		{NewPeeringResource, "infra_peering", []string{"vpc1_id", "vpc2_id"}},
		{NewLoadBalancerResource, "infra_load_balancer", []string{"vpc_id"}},
		{NewLBBackendResource, "infra_lb_backend", []string{"lb_id", "compute_id", "port"}},
		{NewLBTargetGroupResource, "infra_lb_target_group", []string{"vpc_id", "port"}},
		{NewLBTargetResource, "infra_lb_target", []string{"target_group_id", "compute_id"}},
		{NewLBListenerResource, "infra_lb_listener", []string{"load_balancer_id", "port", "protocol", "default_target_group_id"}},
		{NewWAFPolicyResource, "infra_waf_policy", []string{"target_type", "target_id"}},
		{NewWAFRuleResource, "infra_waf_rule", []string{"policy_id", "match_type", "action"}},
		{NewNodeResource, "infra_node", []string{"hostname", "address", "capacity_cpus", "capacity_memory_mb"}},
		{NewNodePoolResource, "infra_node_pool", []string{"name"}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.wantType, func(t *testing.T) {
			t.Parallel()
			r := tc.factory()

			var meta resource.MetadataResponse
			r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "infra"}, &meta)
			if meta.TypeName != tc.wantType {
				t.Fatalf("TypeName = %q, want %q", meta.TypeName, tc.wantType)
			}

			var sresp resource.SchemaResponse
			r.Schema(context.Background(), resource.SchemaRequest{}, &sresp)
			attrs := sresp.Schema.Attributes
			for _, name := range tc.required {
				a, ok := attrs[name]
				if !ok {
					t.Fatalf("missing attribute %q", name)
				}
				if !a.IsRequired() {
					t.Fatalf("attribute %q should be required", name)
				}
			}
			for _, computed := range []string{"id", "phase"} {
				if !attrs[computed].IsComputed() {
					t.Fatalf("attribute %q should be computed", computed)
				}
			}
		})
	}
}

func TestLoadBalancerWithoutPortReadsBackNull(t *testing.T) {
	t.Parallel()

	if !optionalPort(0).IsNull() || optionalPort(443).ValueInt64() != 443 {
		t.Fatal("a load balancer without a port must read back as null, so leaving it out plans no change")
	}
}
