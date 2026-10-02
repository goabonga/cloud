// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package iam_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/iam"
)

// fakeLookup is an in-memory ScopeLookup for tests: projects and folders map
// to a (parentKind, parentID) pair.
type fakeLookup struct {
	projects map[string][2]string
	folders  map[string][2]string
}

func (f fakeLookup) ProjectParent(projectID string) (string, string, error) {
	p, ok := f.projects[projectID]
	if !ok {
		return "", "", errNotFound(projectID)
	}
	return p[0], p[1], nil
}

func (f fakeLookup) FolderParent(folderID string) (string, string, error) {
	p, ok := f.folders[folderID]
	if !ok {
		return "", "", errNotFound(folderID)
	}
	return p[0], p[1], nil
}

type errNotFound string

func (e errNotFound) Error() string { return "not found: " + string(e) }

func TestScopeChainDirectToOrganization(t *testing.T) {
	t.Parallel()

	lookup := fakeLookup{projects: map[string][2]string{
		"project-1": {resource.ParentKindOrganization, "org-1"},
	}}
	chain, err := iam.ScopeChain(lookup, "project-1")
	if err != nil {
		t.Fatalf("ScopeChain: %v", err)
	}
	want := []resource.ObjectReference{
		{Kind: resource.KindProject, UID: "project-1"},
		{Kind: resource.KindOrganization, UID: "org-1"},
	}
	assertChain(t, chain, want)
}

func TestScopeChainThroughOneFolder(t *testing.T) {
	t.Parallel()

	lookup := fakeLookup{
		projects: map[string][2]string{"project-1": {resource.ParentKindFolder, "folder-1"}},
		folders:  map[string][2]string{"folder-1": {resource.ParentKindOrganization, "org-1"}},
	}
	chain, err := iam.ScopeChain(lookup, "project-1")
	if err != nil {
		t.Fatalf("ScopeChain: %v", err)
	}
	want := []resource.ObjectReference{
		{Kind: resource.KindProject, UID: "project-1"},
		{Kind: resource.KindFolder, UID: "folder-1"},
		{Kind: resource.KindOrganization, UID: "org-1"},
	}
	assertChain(t, chain, want)
}

func TestScopeChainThroughNestedFolders(t *testing.T) {
	t.Parallel()

	lookup := fakeLookup{
		projects: map[string][2]string{"project-1": {resource.ParentKindFolder, "folder-2"}},
		folders: map[string][2]string{
			"folder-2": {resource.ParentKindFolder, "folder-1"},
			"folder-1": {resource.ParentKindOrganization, "org-1"},
		},
	}
	chain, err := iam.ScopeChain(lookup, "project-1")
	if err != nil {
		t.Fatalf("ScopeChain: %v", err)
	}
	want := []resource.ObjectReference{
		{Kind: resource.KindProject, UID: "project-1"},
		{Kind: resource.KindFolder, UID: "folder-2"},
		{Kind: resource.KindFolder, UID: "folder-1"},
		{Kind: resource.KindOrganization, UID: "org-1"},
	}
	assertChain(t, chain, want)
}

func TestScopeChainDetectsCycle(t *testing.T) {
	t.Parallel()

	lookup := fakeLookup{
		projects: map[string][2]string{"project-1": {resource.ParentKindFolder, "folder-1"}},
		folders:  map[string][2]string{"folder-1": {resource.ParentKindFolder, "folder-1"}},
	}
	if _, err := iam.ScopeChain(lookup, "project-1"); err == nil {
		t.Fatal("expected an error for a folder that parents itself")
	}
}

func assertChain(t *testing.T, got, want []resource.ObjectReference) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("chain = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chain[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestEffectivePermissionsInheritsFromOrganization(t *testing.T) {
	t.Parallel()

	chain := []resource.ObjectReference{
		{Kind: resource.KindProject, UID: "project-1"},
		{Kind: resource.KindOrganization, UID: "org-1"},
	}
	bindings := []resource.IAMBinding{
		{Spec: resource.IAMBindingSpec{
			Resource: resource.ObjectReference{Kind: resource.KindOrganization, UID: "org-1"},
			Role:     resource.RoleViewer,
			Members:  []string{"user:alice"},
		}},
	}
	got := iam.EffectivePermissions(bindings, chain, "user:alice")
	if !got[iam.PermissionRead] || len(got) != 1 {
		t.Fatalf("got %v, want only read", got)
	}
}

func TestEffectivePermissionsDoesNotLeakToSiblingProject(t *testing.T) {
	t.Parallel()

	chain := []resource.ObjectReference{
		{Kind: resource.KindProject, UID: "project-2"},
		{Kind: resource.KindOrganization, UID: "org-1"},
	}
	bindings := []resource.IAMBinding{
		{Spec: resource.IAMBindingSpec{
			Resource: resource.ObjectReference{Kind: resource.KindProject, UID: "project-1"},
			Role:     resource.RoleOwner,
			Members:  []string{"user:alice"},
		}},
	}
	got := iam.EffectivePermissions(bindings, chain, "user:alice")
	if len(got) != 0 {
		t.Fatalf("expected no permissions, got %v", got)
	}
}

func TestEffectivePermissionsIgnoresOtherSubjects(t *testing.T) {
	t.Parallel()

	chain := []resource.ObjectReference{{Kind: resource.KindProject, UID: "project-1"}}
	bindings := []resource.IAMBinding{
		{Spec: resource.IAMBindingSpec{
			Resource: resource.ObjectReference{Kind: resource.KindProject, UID: "project-1"},
			Role:     resource.RoleOwner,
			Members:  []string{"user:bob"},
		}},
	}
	got := iam.EffectivePermissions(bindings, chain, "user:alice")
	if len(got) != 0 {
		t.Fatalf("expected no permissions for a binding naming a different subject, got %v", got)
	}
}

func TestEffectivePermissionsUnionsMultipleBindings(t *testing.T) {
	t.Parallel()

	chain := []resource.ObjectReference{
		{Kind: resource.KindProject, UID: "project-1"},
		{Kind: resource.KindOrganization, UID: "org-1"},
	}
	bindings := []resource.IAMBinding{
		{Spec: resource.IAMBindingSpec{
			Resource: resource.ObjectReference{Kind: resource.KindOrganization, UID: "org-1"},
			Role:     resource.RoleViewer,
			Members:  []string{"user:alice"},
		}},
		{Spec: resource.IAMBindingSpec{
			Resource: resource.ObjectReference{Kind: resource.KindProject, UID: "project-1"},
			Role:     resource.RoleEditor,
			Members:  []string{"user:alice"},
		}},
	}
	got := iam.EffectivePermissions(bindings, chain, "user:alice")
	for _, p := range []iam.Permission{iam.PermissionRead, iam.PermissionWrite, iam.PermissionDelete} {
		if !got[p] {
			t.Fatalf("expected %v in the union, got %v", p, got)
		}
	}
	if got[iam.PermissionAdmin] {
		t.Fatalf("did not expect admin in the union, got %v", got)
	}
}
