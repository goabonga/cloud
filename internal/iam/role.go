// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package iam resolves the effective permissions an IAM-bound principal has
// on a resource, by walking the Organization > Folder > Project hierarchy
// and unioning the roles bound along the way. It depends on
// internal/domain/resource for role names and resource shapes; the
// dependency never runs the other way.
package iam

import "github.com/goabonga/infrastructure/internal/domain/resource"

// Permission is one capability a role may grant.
type Permission string

// The fixed set of permissions a role bundles.
const (
	PermissionRead   Permission = "read"
	PermissionWrite  Permission = "write"
	PermissionDelete Permission = "delete"
	// PermissionAdmin lets its holder manage iam_bindings at or under the
	// scope it was granted on.
	PermissionAdmin Permission = "admin"
)

var rolePermissions = map[string][]Permission{
	resource.RoleViewer: {PermissionRead},
	resource.RoleEditor: {PermissionRead, PermissionWrite, PermissionDelete},
	resource.RoleOwner:  {PermissionRead, PermissionWrite, PermissionDelete, PermissionAdmin},
}

// Permissions returns the permissions role grants, or nil for an unknown role.
func Permissions(role string) []Permission {
	return rolePermissions[role]
}

// Grants reports whether role includes perm.
func Grants(role string, perm Permission) bool {
	for _, p := range rolePermissions[role] {
		if p == perm {
			return true
		}
	}
	return false
}
