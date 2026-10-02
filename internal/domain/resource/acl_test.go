// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestACLPolicySpecValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rules   []resource.ACLRule
		wantErr bool
	}{
		{"valid allow", []resource.ACLRule{{Action: "allow", Protocol: "tcp", Port: 443, CIDR: "10.0.0.0/8"}}, false},
		{"valid rate-limited", []resource.ACLRule{{Action: "allow", Protocol: "tcp", Port: 80, RateLimit: "10/second"}}, false},
		{"valid deny all", []resource.ACLRule{{Action: "deny"}}, false},
		{"no rules", nil, true},
		{"bad action", []resource.ACLRule{{Action: "maybe"}}, true},
		{"bad protocol", []resource.ACLRule{{Action: "allow", Protocol: "sctp"}}, true},
		{"port on icmp", []resource.ACLRule{{Action: "allow", Protocol: "icmp", Port: 1}}, true},
		{"bad cidr", []resource.ACLRule{{Action: "allow", CIDR: "nope"}}, true},
		{"port out of range", []resource.ACLRule{{Action: "allow", Protocol: "tcp", Port: 70000}}, true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := resource.ACLPolicySpec{Rules: tc.rules}.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestACLPolicySpecSatisfiesValidator(t *testing.T) {
	t.Parallel()

	var _ resource.Validator = resource.ACLPolicySpec{}
}
