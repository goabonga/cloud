// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestVPCSpecValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cidr    string
		wantErr bool
	}{
		{"valid", "10.0.0.0/16", false},
		{"empty", "", true},
		{"garbage", "not-a-cidr", true},
		{"missing mask", "10.0.0.0", true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := resource.VPCSpec{CIDR: tc.cidr}.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for cidr %q", tc.cidr)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for cidr %q: %v", tc.cidr, err)
			}
		})
	}
}

func TestVPCSpecSatisfiesValidator(t *testing.T) {
	t.Parallel()

	var _ resource.Validator = resource.VPCSpec{}
}
