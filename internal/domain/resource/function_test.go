// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestWarmPoolPolicyValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		policy  resource.WarmPoolPolicy
		wantErr bool
	}{
		{"zero value", resource.WarmPoolPolicy{}, false},
		{"min only", resource.WarmPoolPolicy{MinWarm: 2}, false},
		{"min and max", resource.WarmPoolPolicy{MinWarm: 1, MaxWarm: 3}, false},
		{"negative min", resource.WarmPoolPolicy{MinWarm: -1}, true},
		{"negative max", resource.WarmPoolPolicy{MaxWarm: -1}, true},
		{"max below min", resource.WarmPoolPolicy{MinWarm: 3, MaxWarm: 1}, true},
		{"negative ttl", resource.WarmPoolPolicy{IdleTTLSeconds: -1}, true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.policy.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %+v", tc.policy)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for %+v: %v", tc.policy, err)
			}
		})
	}
}

func TestFunctionSpecValidate(t *testing.T) {
	t.Parallel()

	base := resource.FunctionSpec{SubnetID: "sn-1", Image: "example/fn:latest"}

	tests := []struct {
		name    string
		mutate  func(resource.FunctionSpec) resource.FunctionSpec
		wantErr bool
	}{
		{"valid", func(s resource.FunctionSpec) resource.FunctionSpec { return s }, false},
		{"missing subnet", func(s resource.FunctionSpec) resource.FunctionSpec { s.SubnetID = ""; return s }, true},
		{"missing image", func(s resource.FunctionSpec) resource.FunctionSpec { s.Image = ""; return s }, true},
		{"negative cpu", func(s resource.FunctionSpec) resource.FunctionSpec { s.CPU = -1; return s }, true},
		{"negative memory", func(s resource.FunctionSpec) resource.FunctionSpec { s.MemoryMB = -1; return s }, true},
		{"invalid warm pool", func(s resource.FunctionSpec) resource.FunctionSpec {
			s.WarmPool = resource.WarmPoolPolicy{MinWarm: 3, MaxWarm: 1}
			return s
		}, true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.mutate(base).Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestFunctionSpecSatisfiesValidator(t *testing.T) {
	t.Parallel()

	var _ resource.Validator = resource.FunctionSpec{}
}

func TestFunctionInstanceSpecValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		functionID string
		computeID  string
		wantErr    bool
	}{
		{"valid", "fn-1", "compute-1", false},
		{"missing function id", "", "compute-1", true},
		{"missing compute id", "fn-1", "", true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spec := resource.FunctionInstanceSpec{FunctionID: tc.functionID, ComputeID: tc.computeID}
			err := spec.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestFunctionInstanceSpecSatisfiesValidator(t *testing.T) {
	t.Parallel()

	var _ resource.Validator = resource.FunctionInstanceSpec{}
}
