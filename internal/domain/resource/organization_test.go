// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestOrganizationSpecValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		displayName string
		wantErr     bool
	}{
		{"valid", "Acme", false},
		{"empty", "", true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := resource.OrganizationSpec{DisplayName: tc.displayName}.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for displayName %q", tc.displayName)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for displayName %q: %v", tc.displayName, err)
			}
		})
	}
}

func TestOrganizationSpecSatisfiesValidator(t *testing.T) {
	t.Parallel()

	var _ resource.Validator = resource.OrganizationSpec{}
}
