// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestIGWEgressProxyValidation(t *testing.T) {
	t.Parallel()

	igw := func(p resource.EgressProxySpec) resource.IGWSpec {
		return resource.IGWSpec{VPCID: "vpc-1", EgressProxy: &p}
	}
	for _, tc := range []struct {
		name    string
		spec    resource.IGWSpec
		wantErr bool
	}{
		{"no proxy", resource.IGWSpec{VPCID: "vpc-1"}, false},
		{"domains and addresses", igw(resource.EgressProxySpec{Enabled: true,
			AllowedDomains: []string{"example.com", "*.ubuntu.com", "deb.debian.org"},
			AllowedAddresses: []resource.EgressAddress{
				{CIDR: "203.0.113.7"}, {CIDR: "198.51.100.0/24", Protocol: "udp", Port: 123},
			}}), false},
		{"uppercase domain", igw(resource.EgressProxySpec{AllowedDomains: []string{"Example.com"}}), true},
		{"scheme in domain", igw(resource.EgressProxySpec{AllowedDomains: []string{"https://example.com"}}), true},
		{"wildcard in the middle", igw(resource.EgressProxySpec{AllowedDomains: []string{"a.*.example.com"}}), true},
		{"bad cidr", igw(resource.EgressProxySpec{AllowedAddresses: []resource.EgressAddress{{CIDR: "nope"}}}), true},
		{"port without protocol", igw(resource.EgressProxySpec{AllowedAddresses: []resource.EgressAddress{{CIDR: "10.0.0.1", Port: 53}}}), true},
		{"bad protocol", igw(resource.EgressProxySpec{AllowedAddresses: []resource.EgressAddress{{CIDR: "10.0.0.1", Protocol: "icmp"}}}), true},
	} {
		if err := tc.spec.Validate(); (err != nil) != tc.wantErr {
			t.Fatalf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestEgressAddressTakesALoneAddressAsAHost(t *testing.T) {
	t.Parallel()

	n, err := resource.EgressAddress{CIDR: "203.0.113.7"}.Network()
	if err != nil || n.String() != "203.0.113.7/32" {
		t.Fatalf("network %v, %v", n, err)
	}
}
