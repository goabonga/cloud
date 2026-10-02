// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package iam

import (
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
)

// RegistryLookup implements ScopeLookup against the live project and folder
// registries.
type RegistryLookup struct {
	Projects *registry.Registry[resource.ProjectSpec, resource.ProjectStatus]
	Folders  *registry.Registry[resource.FolderSpec, resource.FolderStatus]
}

// ProjectParent implements ScopeLookup.
func (l RegistryLookup) ProjectParent(projectID string) (string, string, error) {
	p, err := l.Projects.Get(projectID)
	if err != nil {
		return "", "", err
	}
	return p.Spec.ParentKind, p.Spec.ParentID, nil
}

// FolderParent implements ScopeLookup.
func (l RegistryLookup) FolderParent(folderID string) (string, string, error) {
	f, err := l.Folders.Get(folderID)
	if err != nil {
		return "", "", err
	}
	return f.Spec.ParentKind, f.Spec.ParentID, nil
}
