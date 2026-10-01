// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestIAMBindingSpecValidate(t *testing.T) {
	t.Parallel()

	validResource := resource.ObjectReference{Kind: resource.KindProject, UID: "project-1"}

	tests := []struct {
		name    string
		spec    resource.IAMBindingSpec
		wantErr bool
	}{
		{
			name: "valid viewer",
			spec: resource.IAMBindingSpec{Resource: validResource, Role: resource.RoleViewer, Members: []string{"user:alice"}},
		},
		{
			name: "valid editor",
			spec: resource.IAMBindingSpec{Resource: validResource, Role: resource.RoleEditor, Members: []string{"user:alice"}},
		},
		{
			name: "valid owner, multiple members",
			spec: resource.IAMBindingSpec{Resource: validResource, Role: resource.RoleOwner, Members: []string{"user:alice", "user:bob"}},
		},
		{
			name:    "missing resource kind",
			spec:    resource.IAMBindingSpec{Resource: resource.ObjectReference{UID: "project-1"}, Role: resource.RoleViewer, Members: []string{"user:alice"}},
			wantErr: true,
		},
		{
			name:    "missing resource uid",
			spec:    resource.IAMBindingSpec{Resource: resource.ObjectReference{Kind: resource.KindProject}, Role: resource.RoleViewer, Members: []string{"user:alice"}},
			wantErr: true,
		},
		{
			name:    "unknown role",
			spec:    resource.IAMBindingSpec{Resource: validResource, Role: "roles/superadmin", Members: []string{"user:alice"}},
			wantErr: true,
		},
		{
			name:    "empty members",
			spec:    resource.IAMBindingSpec{Resource: validResource, Role: resource.RoleViewer},
			wantErr: true,
		},
		{
			name:    "member with no kind prefix",
			spec:    resource.IAMBindingSpec{Resource: validResource, Role: resource.RoleViewer, Members: []string{"alice"}},
			wantErr: true,
		},
		{
			name:    "member with empty id",
			spec:    resource.IAMBindingSpec{Resource: validResource, Role: resource.RoleViewer, Members: []string{"user:"}},
			wantErr: true,
		},
		{
			name:    "member with empty kind",
			spec:    resource.IAMBindingSpec{Resource: validResource, Role: resource.RoleViewer, Members: []string{":alice"}},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.spec.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for spec %+v", tc.spec)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for spec %+v: %v", tc.spec, err)
			}
		})
	}
}

func TestIAMBindingSpecSatisfiesValidator(t *testing.T) {
	t.Parallel()

	var _ resource.Validator = resource.IAMBindingSpec{}
}
