// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "fmt"

// KindOrganization is the resource kind for organizations, the root of the
// Organization > Folder > Project hierarchy. Organizations are not nested.
const KindOrganization = "organization"

// OrganizationSpec is the desired state of an organization.
type OrganizationSpec struct {
	DisplayName string `json:"displayName"`
}

// Validate reports whether the spec is well-formed.
func (s OrganizationSpec) Validate() error {
	if s.DisplayName == "" {
		return fmt.Errorf("organization: displayName is required")
	}
	return nil
}

// OrganizationStatus is the observed state of an organization.
type OrganizationStatus struct {
	StatusBase
}

// Organization is the root of the resource hierarchy.
type Organization = Resource[OrganizationSpec, OrganizationStatus]
