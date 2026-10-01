// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package handler_test

import (
	"net/http"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/handler"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// TestHierarchyKindsCRUD exercises create/get/update/delete/list for each of
// the new hierarchy and IAM kinds through the real generic Handler. No role
// or identity check applies here yet - Phase 2 of the IAM roadmap adds 403
// coverage once the handler becomes authorization-aware; this test only
// proves the generic wiring and each kind's JSON shape.
func TestHierarchyKindsCRUD(t *testing.T) {
	t.Parallel()

	t.Run("organization", func(t *testing.T) {
		t.Parallel()
		reg := registry.New[resource.OrganizationSpec, resource.OrganizationStatus](state.NewFileStore(t.TempDir()), resource.KindOrganization)
		mux := http.NewServeMux()
		handler.New(reg, resource.KindOrganization).Register(mux, "/api/v1")

		in := resource.Organization{Spec: resource.OrganizationSpec{DisplayName: "Acme"}}
		exerciseCRUD(t, mux, "/api/v1/organization", "/api/v1/organization/org-1", in)
	})

	t.Run("folder", func(t *testing.T) {
		t.Parallel()
		reg := registry.New[resource.FolderSpec, resource.FolderStatus](state.NewFileStore(t.TempDir()), resource.KindFolder)
		mux := http.NewServeMux()
		handler.New(reg, resource.KindFolder).Register(mux, "/api/v1")

		in := resource.Folder{Spec: resource.FolderSpec{
			DisplayName: "Engineering",
			ParentRef:   resource.ParentRef{ParentKind: resource.ParentKindOrganization, ParentID: "org-1"},
		}}
		exerciseCRUD(t, mux, "/api/v1/folder", "/api/v1/folder/folder-1", in)
	})

	t.Run("project", func(t *testing.T) {
		t.Parallel()
		reg := registry.New[resource.ProjectSpec, resource.ProjectStatus](state.NewFileStore(t.TempDir()), resource.KindProject)
		mux := http.NewServeMux()
		handler.New(reg, resource.KindProject).Register(mux, "/api/v1")

		in := resource.Project{Spec: resource.ProjectSpec{
			DisplayName: "demo",
			ParentRef:   resource.ParentRef{ParentKind: resource.ParentKindFolder, ParentID: "folder-1"},
		}}
		exerciseCRUD(t, mux, "/api/v1/project", "/api/v1/project/project-1", in)
	})

	t.Run("iam_binding", func(t *testing.T) {
		t.Parallel()
		reg := registry.New[resource.IAMBindingSpec, resource.IAMBindingStatus](state.NewFileStore(t.TempDir()), resource.KindIAMBinding)
		mux := http.NewServeMux()
		handler.New(reg, resource.KindIAMBinding).Register(mux, "/api/v1")

		in := resource.IAMBinding{Spec: resource.IAMBindingSpec{
			Resource: resource.ObjectReference{Kind: resource.KindProject, UID: "project-1"},
			Role:     resource.RoleViewer,
			Members:  []string{"user:alice"},
		}}
		exerciseCRUD(t, mux, "/api/v1/iam_binding", "/api/v1/iam_binding/binding-1", in)
	})
}

// exerciseCRUD drives create, get, update (expecting 200, not 201) and
// delete for one item at itemPath, plus a list on collectionPath before and
// after.
func exerciseCRUD(t *testing.T, mux *http.ServeMux, collectionPath, itemPath string, in any) {
	t.Helper()

	rec := do(t, mux, http.MethodGet, collectionPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty list status = %d", rec.Code)
	}

	rec = do(t, mux, http.MethodPut, itemPath, in)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body %s", rec.Code, rec.Body)
	}

	rec = do(t, mux, http.MethodGet, itemPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d", rec.Code)
	}

	rec = do(t, mux, http.MethodPut, itemPath, in)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200", rec.Code)
	}

	rec = do(t, mux, http.MethodGet, collectionPath, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}

	rec = do(t, mux, http.MethodDelete, itemPath, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}

	rec = do(t, mux, http.MethodGet, itemPath, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get-after-delete status = %d", rec.Code)
	}
}
