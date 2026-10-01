// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package iam_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/iam"
)

func TestPermissions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		role string
		want []iam.Permission
	}{
		{resource.RoleViewer, []iam.Permission{iam.PermissionRead}},
		{resource.RoleEditor, []iam.Permission{iam.PermissionRead, iam.PermissionWrite, iam.PermissionDelete}},
		{resource.RoleOwner, []iam.Permission{iam.PermissionRead, iam.PermissionWrite, iam.PermissionDelete, iam.PermissionAdmin}},
		{"roles/unknown", nil},
	}
	for _, tc := range tests {
		got := iam.Permissions(tc.role)
		if len(got) != len(tc.want) {
			t.Fatalf("Permissions(%q) = %v, want %v", tc.role, got, tc.want)
		}
		for i, p := range tc.want {
			if got[i] != p {
				t.Fatalf("Permissions(%q)[%d] = %v, want %v", tc.role, i, got[i], p)
			}
		}
	}
}

func TestGrants(t *testing.T) {
	t.Parallel()

	if !iam.Grants(resource.RoleOwner, iam.PermissionAdmin) {
		t.Fatal("owner should grant admin")
	}
	if iam.Grants(resource.RoleViewer, iam.PermissionWrite) {
		t.Fatal("viewer should not grant write")
	}
	if iam.Grants("roles/unknown", iam.PermissionRead) {
		t.Fatal("an unknown role should grant nothing")
	}
}
