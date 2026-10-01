// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "fmt"

// KindFolder is the resource kind for folders, an optional grouping layer
// between an Organization and its Projects. Folders may nest under an
// Organization or under another Folder.
const KindFolder = "folder"

// FolderSpec is the desired state of a folder.
type FolderSpec struct {
	DisplayName string `json:"displayName"`
	ParentRef
}

// Validate reports whether the spec is well-formed.
func (s FolderSpec) Validate() error {
	if s.DisplayName == "" {
		return fmt.Errorf("folder: displayName is required")
	}
	if err := s.ParentRef.Validate(ParentKindOrganization, ParentKindFolder); err != nil {
		return fmt.Errorf("folder: %w", err)
	}
	return nil
}

// FolderStatus is the observed state of a folder.
type FolderStatus struct {
	StatusBase
}

// Folder is an optional grouping layer under an Organization.
type Folder = Resource[FolderSpec, FolderStatus]
