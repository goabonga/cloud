// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "fmt"

// Parent kinds a Folder or Project may attach under.
const (
	ParentKindOrganization = "organization"
	ParentKindFolder       = "folder"
)

// ParentRef names the resource a Folder or Project is nested under. A plain
// ID field would be ambiguous here - unlike every other cross-resource
// reference in this package (VPCID, SubnetID, ...), which always points at
// exactly one known kind - since a Folder or a Project may sit directly
// under an Organization or under another Folder.
type ParentRef struct {
	ParentKind string `json:"parentKind"`
	ParentID   string `json:"parentId"`
}

// Validate checks that ParentKind is one of allowed and ParentID is set.
func (p ParentRef) Validate(allowed ...string) error {
	if p.ParentID == "" {
		return fmt.Errorf("parentId is required")
	}
	for _, k := range allowed {
		if p.ParentKind == k {
			return nil
		}
	}
	return fmt.Errorf("parentKind must be one of %v, got %q", allowed, p.ParentKind)
}
