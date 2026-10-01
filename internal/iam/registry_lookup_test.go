// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package iam_test

import (
	"errors"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/iam"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func newRegistryLookup(t *testing.T) iam.RegistryLookup {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	return iam.RegistryLookup{
		Projects: registry.New[resource.ProjectSpec, resource.ProjectStatus](store, resource.KindProject),
		Folders:  registry.New[resource.FolderSpec, resource.FolderStatus](store, resource.KindFolder),
	}
}

func TestRegistryLookupProjectParentedToOrganization(t *testing.T) {
	t.Parallel()

	lookup := newRegistryLookup(t)
	seed := &resource.Project{
		Metadata: resource.ObjectMeta{UID: "project-1"},
		Spec:     resource.ProjectSpec{DisplayName: "demo", ParentRef: resource.ParentRef{ParentKind: resource.ParentKindOrganization, ParentID: "org-1"}},
	}
	if err := lookup.Projects.Put(seed); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	kind, id, err := lookup.ProjectParent("project-1")
	if err != nil {
		t.Fatalf("ProjectParent: %v", err)
	}
	if kind != resource.ParentKindOrganization || id != "org-1" {
		t.Fatalf("got (%q, %q), want (%q, %q)", kind, id, resource.ParentKindOrganization, "org-1")
	}
}

func TestRegistryLookupProjectParentedToFolder(t *testing.T) {
	t.Parallel()

	lookup := newRegistryLookup(t)
	seed := &resource.Project{
		Metadata: resource.ObjectMeta{UID: "project-1"},
		Spec:     resource.ProjectSpec{DisplayName: "demo", ParentRef: resource.ParentRef{ParentKind: resource.ParentKindFolder, ParentID: "folder-1"}},
	}
	if err := lookup.Projects.Put(seed); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	kind, id, err := lookup.ProjectParent("project-1")
	if err != nil {
		t.Fatalf("ProjectParent: %v", err)
	}
	if kind != resource.ParentKindFolder || id != "folder-1" {
		t.Fatalf("got (%q, %q), want (%q, %q)", kind, id, resource.ParentKindFolder, "folder-1")
	}
}

func TestRegistryLookupFolderParent(t *testing.T) {
	t.Parallel()

	lookup := newRegistryLookup(t)
	seed := &resource.Folder{
		Metadata: resource.ObjectMeta{UID: "folder-1"},
		Spec:     resource.FolderSpec{DisplayName: "eng", ParentRef: resource.ParentRef{ParentKind: resource.ParentKindOrganization, ParentID: "org-1"}},
	}
	if err := lookup.Folders.Put(seed); err != nil {
		t.Fatalf("seed folder: %v", err)
	}

	kind, id, err := lookup.FolderParent("folder-1")
	if err != nil {
		t.Fatalf("FolderParent: %v", err)
	}
	if kind != resource.ParentKindOrganization || id != "org-1" {
		t.Fatalf("got (%q, %q), want (%q, %q)", kind, id, resource.ParentKindOrganization, "org-1")
	}
}

func TestRegistryLookupMissingProjectReturnsNotFound(t *testing.T) {
	t.Parallel()

	lookup := newRegistryLookup(t)
	_, _, err := lookup.ProjectParent("does-not-exist")
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected state.ErrNotFound, got %v", err)
	}
}

func TestRegistryLookupMissingFolderReturnsNotFound(t *testing.T) {
	t.Parallel()

	lookup := newRegistryLookup(t)
	_, _, err := lookup.FolderParent("does-not-exist")
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected state.ErrNotFound, got %v", err)
	}
}
