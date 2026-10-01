// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import (
	"fmt"
	"strings"
)

// KindIAMBinding is the resource kind for IAM policy bindings: a role
// granted to a set of members on a resource in the hierarchy.
const KindIAMBinding = "iam_binding"

// The fixed, Go-defined set of roles available in this release. Custom roles
// are a deliberate non-goal: each name bundles a fixed set of permissions,
// resolved by package iam.
const (
	RoleViewer = "roles/viewer"
	RoleEditor = "roles/editor"
	RoleOwner  = "roles/owner"
)

// KnownRoles lists every role a binding may grant.
var KnownRoles = []string{RoleViewer, RoleEditor, RoleOwner}

// MemberPrefixUser marks a member as a user subject, as opposed to a future
// principal kind (group, serviceAccount, ...). A plain "user:<uid>" prefix
// is required from the start so adding those kinds later needs no breaking
// migration of stored bindings.
const MemberPrefixUser = "user"

// IAMBindingSpec grants Role to every member on Resource. Resource may name
// an organization, a folder, a project, or (for one-off sharing) any other
// resource.
type IAMBindingSpec struct {
	Resource ObjectReference `json:"resource"`
	Role     string          `json:"role"`
	Members  []string        `json:"members"`
}

// Validate reports whether the spec is well-formed.
func (s IAMBindingSpec) Validate() error {
	if s.Resource.Kind == "" || s.Resource.UID == "" {
		return fmt.Errorf("iam_binding: resource.kind and resource.uid are required")
	}
	valid := false
	for _, r := range KnownRoles {
		if s.Role == r {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("iam_binding: role must be one of %v, got %q", KnownRoles, s.Role)
	}
	if len(s.Members) == 0 {
		return fmt.Errorf("iam_binding: members must not be empty")
	}
	for _, m := range s.Members {
		prefix, id, ok := strings.Cut(m, ":")
		if !ok || prefix == "" || id == "" {
			return fmt.Errorf("iam_binding: member %q must be formatted as \"<kind>:<id>\"", m)
		}
	}
	return nil
}

// IAMBindingStatus is the observed state of an IAM binding. Bindings take
// effect immediately on write, so there is nothing for a controller to
// reconcile; the field exists only so IAMBinding fits the generic envelope.
type IAMBindingStatus struct {
	StatusBase
}

// IAMBinding grants a role to a set of members on a resource.
type IAMBinding = Resource[IAMBindingSpec, IAMBindingStatus]
