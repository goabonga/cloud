// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package iam_test

import (
	"errors"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/iam"
)

func TestAuthorizerAdminBypasses(t *testing.T) {
	t.Parallel()

	az := &iam.Authorizer{Lookup: failingLookup{}, Bindings: emptyBindings}
	allowed, err := az.Allowed("alice", true, "", "project-1", iam.PermissionDelete)
	if err != nil || !allowed {
		t.Fatalf("admin should bypass everything, got %v, %v", allowed, err)
	}
}

func TestAuthorizerOwnerFastPathWithNoBindingAtAll(t *testing.T) {
	t.Parallel()

	az := &iam.Authorizer{Lookup: failingLookup{}, Bindings: emptyBindings}
	allowed, err := az.Allowed("alice", false, "alice", "project-1", iam.PermissionDelete)
	if err != nil || !allowed {
		t.Fatalf("owner should be allowed without any binding or lookup, got %v, %v", allowed, err)
	}
}

func TestAuthorizerUnscopedNonOwnerDenied(t *testing.T) {
	t.Parallel()

	az := &iam.Authorizer{Lookup: failingLookup{}, Bindings: emptyBindings}
	allowed, err := az.Allowed("alice", false, "bob", "", iam.PermissionRead)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Fatal("a non-owner should not reach an unscoped resource")
	}
}

func TestAuthorizerDirectProjectBinding(t *testing.T) {
	t.Parallel()

	lookup := fakeLookup{projects: map[string][2]string{"project-1": {resource.ParentKindOrganization, "org-1"}}}
	bindings := []resource.IAMBinding{{Spec: resource.IAMBindingSpec{
		Resource: resource.ObjectReference{Kind: resource.KindProject, UID: "project-1"},
		Role:     resource.RoleEditor,
		Members:  []string{"user:alice"},
	}}}
	az := &iam.Authorizer{Lookup: lookup, Bindings: func() ([]resource.IAMBinding, error) { return bindings, nil }}

	allowed, err := az.Allowed("alice", false, "bob", "project-1", iam.PermissionWrite)
	if err != nil || !allowed {
		t.Fatalf("expected the direct project binding to grant write, got %v, %v", allowed, err)
	}
}

func TestAuthorizerInheritedFromFolder(t *testing.T) {
	t.Parallel()

	lookup := fakeLookup{
		projects: map[string][2]string{"project-1": {resource.ParentKindFolder, "folder-1"}},
		folders:  map[string][2]string{"folder-1": {resource.ParentKindOrganization, "org-1"}},
	}
	bindings := []resource.IAMBinding{{Spec: resource.IAMBindingSpec{
		Resource: resource.ObjectReference{Kind: resource.KindFolder, UID: "folder-1"},
		Role:     resource.RoleViewer,
		Members:  []string{"user:alice"},
	}}}
	az := &iam.Authorizer{Lookup: lookup, Bindings: func() ([]resource.IAMBinding, error) { return bindings, nil }}

	allowed, err := az.Allowed("alice", false, "", "project-1", iam.PermissionRead)
	if err != nil || !allowed {
		t.Fatalf("expected the folder binding to be inherited, got %v, %v", allowed, err)
	}
}

func TestAuthorizerInheritedFromOrganizationThroughNestedFolders(t *testing.T) {
	t.Parallel()

	lookup := fakeLookup{
		projects: map[string][2]string{"project-1": {resource.ParentKindFolder, "folder-2"}},
		folders: map[string][2]string{
			"folder-2": {resource.ParentKindFolder, "folder-1"},
			"folder-1": {resource.ParentKindOrganization, "org-1"},
		},
	}
	bindings := []resource.IAMBinding{{Spec: resource.IAMBindingSpec{
		Resource: resource.ObjectReference{Kind: resource.KindOrganization, UID: "org-1"},
		Role:     resource.RoleViewer,
		Members:  []string{"user:alice"},
	}}}
	az := &iam.Authorizer{Lookup: lookup, Bindings: func() ([]resource.IAMBinding, error) { return bindings, nil }}

	allowed, err := az.Allowed("alice", false, "", "project-1", iam.PermissionRead)
	if err != nil || !allowed {
		t.Fatalf("expected the organization binding to be inherited through two folders, got %v, %v", allowed, err)
	}
}

func TestAuthorizerLookupErrorPropagates(t *testing.T) {
	t.Parallel()

	az := &iam.Authorizer{Lookup: failingLookup{}, Bindings: emptyBindings}
	_, err := az.Allowed("alice", false, "", "project-1", iam.PermissionRead)
	if err == nil {
		t.Fatal("expected the lookup error to propagate")
	}
}

func emptyBindings() ([]resource.IAMBinding, error) { return nil, nil }

type failingLookup struct{}

func (failingLookup) ProjectParent(string) (string, string, error) { return "", "", errors.New("boom") }
func (failingLookup) FolderParent(string) (string, string, error)  { return "", "", errors.New("boom") }

func TestRequestAuthorizationCacheRefreshesAfterRevocation(t *testing.T) {
	reads := 0
	bindings := []resource.IAMBinding{{Spec: resource.IAMBindingSpec{Resource: resource.ObjectReference{Kind: resource.KindProject, UID: "project-1"}, Role: resource.RoleViewer, Members: []string{"user:alice"}}}}
	az := &iam.Authorizer{Lookup: fakeLookup{projects: map[string][2]string{"project-1": {resource.ParentKindOrganization, "org-1"}}}, Bindings: func() ([]resource.IAMBinding, error) { reads++; return bindings, nil }}
	request := az.ForRequest()
	for range 100 {
		allowed, err := request.Allowed("alice", false, "bob", "project-1", iam.PermissionRead)
		if !allowed || err != nil {
			t.Fatalf("authorization=%v,%v", allowed, err)
		}
	}
	if reads != 1 {
		t.Fatalf("bindings read %d times", reads)
	}
	allowed, err := request.Allowed("carol", false, "bob", "project-1", iam.PermissionRead)
	if allowed || err != nil {
		t.Fatal("cached decision leaked to another subject")
	}
	allowed, err = request.Allowed("alice", false, "bob", "project-1", iam.PermissionWrite)
	if allowed || err != nil {
		t.Fatal("cached read permission granted write")
	}
	bindings = nil
	allowed, err = az.ForRequest().Allowed("alice", false, "bob", "project-1", iam.PermissionRead)
	if allowed || err != nil {
		t.Fatal("revocation was hidden by cross-request cache")
	}
	if reads != 2 {
		t.Fatalf("bindings read %d times after revocation", reads)
	}
}
