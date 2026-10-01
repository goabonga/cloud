// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestTopologySpecsSatisfyValidator(t *testing.T) {
	t.Parallel()

	var (
		_ resource.Validator = resource.SubnetSpec{}
		_ resource.Validator = resource.SecurityGroupSpec{}
		_ resource.Validator = resource.SecurityGroupRuleSpec{}
		_ resource.Validator = resource.IGWSpec{}
		_ resource.Validator = resource.RouteSpec{}
		_ resource.Validator = resource.KMSKeyringSpec{}
		_ resource.Validator = resource.KMSKeySpec{}
		_ resource.Validator = resource.DiskSpec{}
		_ resource.Validator = resource.DiskFileSpec{}
		_ resource.Validator = resource.ComputeSpec{}
		_ resource.Validator = resource.DNSZoneSpec{}
		_ resource.Validator = resource.DNSRecordSpec{}
		_ resource.Validator = resource.PeeringSpec{}
		_ resource.Validator = resource.LoadBalancerSpec{}
		_ resource.Validator = resource.LBBackendSpec{}
		_ resource.Validator = resource.WAFPolicySpec{}
		_ resource.Validator = resource.WAFRuleSpec{}
		_ resource.Validator = resource.NodeSpec{}
		_ resource.Validator = resource.NodePoolSpec{}
		_ resource.Validator = resource.SecretVersionSpec{}
		_ resource.Validator = resource.SSLCertSpec{}
	)
}

func TestTopologyValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		spec    resource.Validator
		wantErr bool
	}{
		{"subnet ok", resource.SubnetSpec{VPCID: "vpc-1", CIDR: "10.0.1.0/24", Type: "public"}, false},
		{"subnet no vpc", resource.SubnetSpec{CIDR: "10.0.1.0/24"}, true},
		{"subnet bad cidr", resource.SubnetSpec{VPCID: "vpc-1", CIDR: "x"}, true},
		{"subnet bad type", resource.SubnetSpec{VPCID: "vpc-1", CIDR: "10.0.1.0/24", Type: "dmz"}, true},
		{"sg ok", resource.SecurityGroupSpec{VPCID: "vpc-1"}, false},
		{"sg no vpc", resource.SecurityGroupSpec{}, true},
		{"rule ok", resource.SecurityGroupRuleSpec{SecurityGroupID: "sg-1", Direction: "ingress", Protocol: "tcp", Port: 443}, false},
		{"rule bad dir", resource.SecurityGroupRuleSpec{SecurityGroupID: "sg-1", Direction: "x", Protocol: "tcp"}, true},
		{"igw ok", resource.IGWSpec{VPCID: "vpc-1"}, false},
		{"route ok", resource.RouteSpec{VPCID: "vpc-1", Destination: "0.0.0.0/0", Gateway: "igw-1"}, false},
		{"route bad dest", resource.RouteSpec{VPCID: "vpc-1", Destination: "x", Gateway: "igw-1"}, true},
		{"keyring ok", resource.KMSKeyringSpec{Name: "prod"}, false},
		{"key no ring", resource.KMSKeySpec{Name: "k"}, true},
		{"disk ok", resource.DiskSpec{SizeMB: 1024, KMSKeyID: "key-1"}, false},
		{"disk bad size", resource.DiskSpec{SizeMB: 0}, true},
		{"diskfile ok", resource.DiskFileSpec{DiskID: "disk-1", Path: "/etc/x"}, false},
		{"diskfile from a cert", resource.DiskFileSpec{DiskID: "disk-1", Path: "tls.crt", SSLCertID: "c", SSLPart: resource.SSLPartChain}, false},
		{"diskfile content and cert", resource.DiskFileSpec{DiskID: "disk-1", Path: "x", Content: "x", SSLCertID: "c", SSLPart: resource.SSLPartCertificate}, true},
		{"diskfile bad part", resource.DiskFileSpec{DiskID: "disk-1", Path: "x", SSLCertID: "c", SSLPart: "pfx"}, true},
		{"diskfile part without cert", resource.DiskFileSpec{DiskID: "disk-1", Path: "x", SSLPart: resource.SSLPartPrivateKey}, true},
		{"compute ok", resource.ComputeSpec{SubnetID: "sn-1", Image: "nginx:latest"}, false},
		{"compute no subnet", resource.ComputeSpec{Image: "nginx"}, true},
		{"compute no image", resource.ComputeSpec{SubnetID: "sn-1"}, true},
		{"compute bad disk", resource.ComputeSpec{SubnetID: "sn-1", Image: "nginx", Disks: []resource.ComputeDiskRef{{DiskID: "d"}}}, true},
		{"dns zone ok", resource.DNSZoneSpec{Domain: "example.com", Visibility: "private"}, false},
		{"dns zone no domain", resource.DNSZoneSpec{}, true},
		{"dns zone bad visibility", resource.DNSZoneSpec{Domain: "example.com", Visibility: "dmz"}, true},
		{"dns record ok", resource.DNSRecordSpec{ZoneID: "z-1", Name: "www", Type: "A", Records: []string{"10.0.0.5"}}, false},
		{"dns record bad type", resource.DNSRecordSpec{ZoneID: "z-1", Type: "ZZ", Records: []string{"x"}}, true},
		{"dns record no values", resource.DNSRecordSpec{ZoneID: "z-1", Type: "A"}, true},
		{"peering ok", resource.PeeringSpec{VPC1ID: "vpc-1", VPC2ID: "vpc-2"}, false},
		{"peering self", resource.PeeringSpec{VPC1ID: "vpc-1", VPC2ID: "vpc-1"}, true},
		{"lb ok", resource.LoadBalancerSpec{VPCID: "vpc-1", Port: 443, Protocol: "tcp", Algorithm: "round_robin"}, false},
		{"lb bad port", resource.LoadBalancerSpec{VPCID: "vpc-1", Port: 0}, true},
		{"lb bad algo", resource.LoadBalancerSpec{VPCID: "vpc-1", Port: 80, Algorithm: "magic"}, true},
		{"lb backend ok", resource.LBBackendSpec{LBID: "lb-1", ComputeID: "i-1", Port: 8080}, false},
		{"lb backend no compute", resource.LBBackendSpec{LBID: "lb-1", Port: 8080}, true},
		{"waf policy ok", resource.WAFPolicySpec{TargetType: "compute", TargetID: "i-1"}, false},
		{"waf policy bad target", resource.WAFPolicySpec{TargetType: "host", TargetID: "i-1"}, true},
		{"waf rule ok", resource.WAFRuleSpec{PolicyID: "w-1", MatchType: "path", Action: "block"}, false},
		{"waf rule ratelimit no limit", resource.WAFRuleSpec{PolicyID: "w-1", MatchType: "path", Action: "ratelimit"}, true},
		{"node ok", resource.NodeSpec{Hostname: "h1", Address: "10.0.0.2", Capacity: resource.NodeCapacity{CPUs: 4, MemoryMB: 8192}}, false},
		{"node no capacity", resource.NodeSpec{Hostname: "h1", Address: "10.0.0.2"}, true},
		{"node pool ok", resource.NodePoolSpec{Name: "default", MinNodes: 1, MaxNodes: 3}, false},
		{"node pool bad range", resource.NodePoolSpec{Name: "default", MinNodes: 5, MaxNodes: 2}, true},
		{"secret version ok", resource.SecretVersionSpec{SecretID: "sec-1", Data: "x"}, false},
		{"secret version no secret", resource.SecretVersionSpec{Data: "x"}, true},
		{"secret version no data", resource.SecretVersionSpec{SecretID: "sec-1"}, true},
		{"ssl cert ok", resource.SSLCertSpec{CAID: "ca-1", CommonName: "web.example.com"}, false},
		{"ssl cert no ca", resource.SSLCertSpec{CommonName: "web.example.com"}, true},
		{"ssl cert no cn", resource.SSLCertSpec{CAID: "ca-1"}, true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.spec.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
