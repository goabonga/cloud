// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "fmt"

// KindProject is the resource kind for projects, the primary unit of
// isolation, IAM, quotas and billing (see ObjectMeta.ProjectID). A project
// attaches directly to an Organization or to a Folder.
const KindProject = "project"

// ProjectSpec is the desired state of a project.
type ProjectSpec struct {
	DisplayName string `json:"displayName"`
	ParentRef
}

// Validate reports whether the spec is well-formed.
func (s ProjectSpec) Validate() error {
	if s.DisplayName == "" {
		return fmt.Errorf("project: displayName is required")
	}
	if err := s.ParentRef.Validate(ParentKindOrganization, ParentKindFolder); err != nil {
		return fmt.Errorf("project: %w", err)
	}
	return nil
}

// ProjectStatus is the observed state of a project.
type ProjectStatus struct {
	StatusBase
}

// Project is the unit every other resource kind scopes itself to via
// ObjectMeta.ProjectID.
type Project = Resource[ProjectSpec, ProjectStatus]
