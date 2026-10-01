// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goabonga/infrastructure/internal/auth"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/handler"
	"github.com/goabonga/infrastructure/internal/iam"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// newAuthzVPC wires a VPC handler behind an Authorizer backed by real
// project/folder registries, so scope-chain resolution is exercised
// end-to-end rather than through a fake. bindings is returned live by the
// Authorizer on every call, so a test may seed it once up front.
func newAuthzVPC(t *testing.T, bindings []resource.IAMBinding) (mux *http.ServeMux, vpcs *registry.Registry[resource.VPCSpec, resource.VPCStatus], projects *registry.Registry[resource.ProjectSpec, resource.ProjectStatus]) {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	vpcs = registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC)
	projects = registry.New[resource.ProjectSpec, resource.ProjectStatus](store, resource.KindProject)
	folders := registry.New[resource.FolderSpec, resource.FolderStatus](store, resource.KindFolder)
	az := &iam.Authorizer{
		Lookup:   iam.RegistryLookup{Projects: projects, Folders: folders},
		Bindings: func() ([]resource.IAMBinding, error) { return bindings, nil },
	}
	mux = http.NewServeMux()
	handler.New(vpcs, resource.KindVPC, handler.WithAuthorization[resource.VPCSpec, resource.VPCStatus](az)).Register(mux, "/api/v1")
	return mux, vpcs, projects
}

func seedProject(t *testing.T, projects *registry.Registry[resource.ProjectSpec, resource.ProjectStatus], uid string) {
	t.Helper()
	err := projects.Put(&resource.Project{
		Metadata: resource.ObjectMeta{UID: uid},
		Spec: resource.ProjectSpec{
			DisplayName: uid,
			ParentRef:   resource.ParentRef{ParentKind: resource.ParentKindOrganization, ParentID: "org-1"},
		},
	})
	if err != nil {
		t.Fatalf("seed project %s: %v", uid, err)
	}
}

// doAs issues a request carrying subject/roles as the authenticated identity.
func doAs(t *testing.T, mux *http.ServeMux, subject string, roles []string, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req = req.WithContext(auth.WithIdentity(req.Context(), &auth.Identity{Subject: subject, Roles: roles}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAuthorizationCreateIntoProjectRequiresAGrant(t *testing.T) {
	t.Parallel()

	mux, _, projects := newAuthzVPC(t, nil)
	seedProject(t, projects, "project-1")

	in := resource.VPC{Metadata: resource.ObjectMeta{ProjectID: "project-1"}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	rec := doAs(t, mux, "alice", nil, http.MethodPut, "/api/v1/vpc/vpc-1", in)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 with no grant at all", rec.Code)
	}
}

func TestAuthorizationViewerRoleCannotCreate(t *testing.T) {
	t.Parallel()

	bindings := []resource.IAMBinding{{Spec: resource.IAMBindingSpec{
		Resource: resource.ObjectReference{Kind: resource.KindProject, UID: "project-1"},
		Role:     resource.RoleViewer,
		Members:  []string{"user:alice"},
	}}}
	mux, _, projects := newAuthzVPC(t, bindings)
	seedProject(t, projects, "project-1")

	in := resource.VPC{Metadata: resource.ObjectMeta{ProjectID: "project-1"}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	rec := doAs(t, mux, "alice", nil, http.MethodPut, "/api/v1/vpc/vpc-1", in)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: a viewer grant does not include write", rec.Code)
	}
}

func TestAuthorizationEditorRoleCanCreateAndBecomesOwner(t *testing.T) {
	t.Parallel()

	bindings := []resource.IAMBinding{{Spec: resource.IAMBindingSpec{
		Resource: resource.ObjectReference{Kind: resource.KindProject, UID: "project-1"},
		Role:     resource.RoleEditor,
		Members:  []string{"user:alice"},
	}}}
	mux, vpcs, projects := newAuthzVPC(t, bindings)
	seedProject(t, projects, "project-1")

	// A client-supplied ownerUid must never be honored.
	in := resource.VPC{
		Metadata: resource.ObjectMeta{ProjectID: "project-1", OwnerUID: "someone-else"},
		Spec:     resource.VPCSpec{CIDR: "10.0.0.0/16"},
	}
	rec := doAs(t, mux, "alice", nil, http.MethodPut, "/api/v1/vpc/vpc-1", in)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s, want 201", rec.Code, rec.Body)
	}
	stored, err := vpcs.Get("vpc-1")
	if err != nil {
		t.Fatalf("get stored: %v", err)
	}
	if stored.Metadata.OwnerUID != "alice" {
		t.Fatalf("ownerUid = %q, want the creator, not the client-supplied value", stored.Metadata.OwnerUID)
	}
}

func TestAuthorizationUnscopedCreateAlwaysAllowed(t *testing.T) {
	t.Parallel()

	mux, vpcs, _ := newAuthzVPC(t, nil)

	in := resource.VPC{Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	rec := doAs(t, mux, "alice", nil, http.MethodPut, "/api/v1/vpc/vpc-1", in)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s, want 201 for an unscoped create", rec.Code, rec.Body)
	}
	stored, err := vpcs.Get("vpc-1")
	if err != nil || stored.Metadata.OwnerUID != "alice" {
		t.Fatalf("stored = %+v, %v, want ownerUid alice", stored, err)
	}
}

func TestAuthorizationOwnerCanGetUpdateDeleteWithoutAnyBinding(t *testing.T) {
	t.Parallel()

	mux, _, _ := newAuthzVPC(t, nil)

	in := resource.VPC{Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	if rec := doAs(t, mux, "alice", nil, http.MethodPut, "/api/v1/vpc/vpc-1", in); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d", rec.Code)
	}
	if rec := doAs(t, mux, "alice", nil, http.MethodGet, "/api/v1/vpc/vpc-1", nil); rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, want the owner to read their own resource", rec.Code)
	}
	in.Spec.CIDR = "10.1.0.0/16"
	if rec := doAs(t, mux, "alice", nil, http.MethodPut, "/api/v1/vpc/vpc-1", in); rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want the owner to update their own resource", rec.Code)
	}
	if rec := doAs(t, mux, "alice", nil, http.MethodDelete, "/api/v1/vpc/vpc-1", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want the owner to delete their own resource", rec.Code)
	}
}

func TestAuthorizationUnboundSubjectCannotTouchSomeoneElsesResource(t *testing.T) {
	t.Parallel()

	mux, _, _ := newAuthzVPC(t, nil)

	in := resource.VPC{Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	if rec := doAs(t, mux, "alice", nil, http.MethodPut, "/api/v1/vpc/vpc-1", in); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d", rec.Code)
	}

	if rec := doAs(t, mux, "bob", nil, http.MethodGet, "/api/v1/vpc/vpc-1", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("get status = %d, want 403 for an unrelated caller", rec.Code)
	}
	if rec := doAs(t, mux, "bob", nil, http.MethodPut, "/api/v1/vpc/vpc-1", in); rec.Code != http.StatusForbidden {
		t.Fatalf("update status = %d, want 403 for an unrelated caller", rec.Code)
	}
	if rec := doAs(t, mux, "bob", nil, http.MethodDelete, "/api/v1/vpc/vpc-1", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("delete status = %d, want 403 for an unrelated caller", rec.Code)
	}
}

func TestAuthorizationProjectIDIsImmutable(t *testing.T) {
	t.Parallel()

	mux, _, projects := newAuthzVPC(t, nil)
	seedProject(t, projects, "project-1")
	seedProject(t, projects, "project-2")

	in := resource.VPC{Metadata: resource.ObjectMeta{ProjectID: "project-1"}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	// alice needs no grant: an unscoped-then-reparented attempt starts from
	// a project she doesn't have write on, so seed it as an unscoped
	// resource she owns instead, then try to move it into a project.
	unscoped := resource.VPC{Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	if rec := doAs(t, mux, "alice", nil, http.MethodPut, "/api/v1/vpc/vpc-1", unscoped); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d", rec.Code)
	}
	if rec := doAs(t, mux, "alice", nil, http.MethodPut, "/api/v1/vpc/vpc-1", in); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 when changing projectId", rec.Code)
	}
}

func TestAuthorizationAdminBypassesEveryCheck(t *testing.T) {
	t.Parallel()

	mux, _, projects := newAuthzVPC(t, nil)
	seedProject(t, projects, "project-1")

	in := resource.VPC{Metadata: resource.ObjectMeta{ProjectID: "project-1"}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	admin := []string{"admin"}
	if rec := doAs(t, mux, "root", admin, http.MethodPut, "/api/v1/vpc/vpc-1", in); rec.Code != http.StatusCreated {
		t.Fatalf("admin create status = %d, body %s", rec.Code, rec.Body)
	}
	if rec := doAs(t, mux, "someone-else", admin, http.MethodGet, "/api/v1/vpc/vpc-1", nil); rec.Code != http.StatusOK {
		t.Fatalf("admin get status = %d", rec.Code)
	}
	if rec := doAs(t, mux, "someone-else", admin, http.MethodDelete, "/api/v1/vpc/vpc-1", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete status = %d", rec.Code)
	}
}

func TestAuthorizationListFiltersToTheVisibleSubset(t *testing.T) {
	t.Parallel()

	bindings := []resource.IAMBinding{
		{Spec: resource.IAMBindingSpec{
			Resource: resource.ObjectReference{Kind: resource.KindProject, UID: "project-1"},
			Role:     resource.RoleViewer,
			Members:  []string{"user:carol"},
		}},
		// bob needs write on project-1 to create the shared vpc below.
		{Spec: resource.IAMBindingSpec{
			Resource: resource.ObjectReference{Kind: resource.KindProject, UID: "project-1"},
			Role:     resource.RoleEditor,
			Members:  []string{"user:bob"},
		}},
	}
	mux, _, projects := newAuthzVPC(t, bindings)
	seedProject(t, projects, "project-1")

	// alice's own unscoped vpc.
	if rec := doAs(t, mux, "alice", nil, http.MethodPut, "/api/v1/vpc/mine", resource.VPC{Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}); rec.Code != http.StatusCreated {
		t.Fatalf("create mine status = %d", rec.Code)
	}
	// bob's project-scoped vpc, shared with carol via the viewer binding above.
	bobsVPC := resource.VPC{Metadata: resource.ObjectMeta{ProjectID: "project-1"}, Spec: resource.VPCSpec{CIDR: "10.1.0.0/16"}}
	if rec := doAs(t, mux, "bob", nil, http.MethodPut, "/api/v1/vpc/shared", bobsVPC); rec.Code != http.StatusCreated {
		t.Fatalf("create shared status = %d, body %s", rec.Code, rec.Body)
	}
	// A third, untouched vpc bob owns but never shares.
	if rec := doAs(t, mux, "bob", nil, http.MethodPut, "/api/v1/vpc/private", resource.VPC{Spec: resource.VPCSpec{CIDR: "10.2.0.0/16"}}); rec.Code != http.StatusCreated {
		t.Fatalf("create private status = %d", rec.Code)
	}

	rec := doAs(t, mux, "carol", nil, http.MethodGet, "/api/v1/vpc", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var list resource.List[resource.VPCSpec, resource.VPCStatus]
	mustDecode(t, rec, &list)
	uids := make(map[string]bool, len(list.Items))
	for _, it := range list.Items {
		uids[it.Metadata.UID] = true
	}
	if uids["mine"] || uids["private"] {
		t.Fatalf("carol should not see alice's or bob's unshared resources, got %v", uids)
	}
	if !uids["shared"] {
		t.Fatalf("carol should see the vpc shared with her via the viewer binding, got %v", uids)
	}
}

func TestAuthorizationHierarchyKindOwnerOnly(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	orgs := registry.New[resource.OrganizationSpec, resource.OrganizationStatus](store, resource.KindOrganization)
	projects := registry.New[resource.ProjectSpec, resource.ProjectStatus](store, resource.KindProject)
	folders := registry.New[resource.FolderSpec, resource.FolderStatus](store, resource.KindFolder)
	az := &iam.Authorizer{
		Lookup:   iam.RegistryLookup{Projects: projects, Folders: folders},
		Bindings: func() ([]resource.IAMBinding, error) { return nil, nil },
	}
	mux := http.NewServeMux()
	handler.New(orgs, resource.KindOrganization, handler.WithAuthorization[resource.OrganizationSpec, resource.OrganizationStatus](az)).Register(mux, "/api/v1")

	in := resource.Organization{Spec: resource.OrganizationSpec{DisplayName: "Acme"}}
	if rec := doAs(t, mux, "alice", nil, http.MethodPut, "/api/v1/organization/org-1", in); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body %s", rec.Code, rec.Body)
	}
	if rec := doAs(t, mux, "alice", nil, http.MethodGet, "/api/v1/organization/org-1", nil); rec.Code != http.StatusOK {
		t.Fatalf("owner get status = %d", rec.Code)
	}
	if rec := doAs(t, mux, "bob", nil, http.MethodGet, "/api/v1/organization/org-1", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("unrelated get status = %d, want 403", rec.Code)
	}
	if rec := doAs(t, mux, "root", []string{"admin"}, http.MethodGet, "/api/v1/organization/org-1", nil); rec.Code != http.StatusOK {
		t.Fatalf("admin get status = %d", rec.Code)
	}
}

func TestAuthorizationUnauthenticatedRequestIsForbidden(t *testing.T) {
	t.Parallel()

	mux, _, _ := newAuthzVPC(t, nil)

	// Seed as alice so the record exists; an unauthenticated get must still be
	// refused even though it isn't a 404.
	in := resource.VPC{Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	if rec := doAs(t, mux, "alice", nil, http.MethodPut, "/api/v1/vpc/vpc-1", in); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d", rec.Code)
	}

	// do() issues the request with no identity in context at all.
	if rec := do(t, mux, http.MethodGet, "/api/v1/vpc/vpc-1", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated get status = %d, want 403", rec.Code)
	}
}

func TestAuthorizationUnauthenticatedCreateIsForbidden(t *testing.T) {
	t.Parallel()

	mux, _, _ := newAuthzVPC(t, nil)

	in := resource.VPC{Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	if rec := do(t, mux, http.MethodPut, "/api/v1/vpc/vpc-1", in); rec.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated create status = %d, want 403", rec.Code)
	}
}

func TestAuthorizationAdminOnlyHasNoSelfServicePath(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	bindings := registry.New[resource.IAMBindingSpec, resource.IAMBindingStatus](store, resource.KindIAMBinding)
	mux := http.NewServeMux()
	handler.New(bindings, resource.KindIAMBinding, handler.WithAdminOnly[resource.IAMBindingSpec, resource.IAMBindingStatus]()).Register(mux, "/api/v1")

	in := resource.IAMBinding{Spec: resource.IAMBindingSpec{
		Resource: resource.ObjectReference{Kind: resource.KindProject, UID: "project-1"},
		Role:     resource.RoleOwner,
		Members:  []string{"user:alice"},
	}}
	// Unlike an ordinary unscoped resource, a non-admin may not create an
	// iam_binding even naming themselves - that would be a self-grant.
	if rec := doAs(t, mux, "alice", nil, http.MethodPut, "/api/v1/iam_binding/binding-1", in); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin create status = %d, want 403", rec.Code)
	}
	if rec := doAs(t, mux, "root", []string{"admin"}, http.MethodPut, "/api/v1/iam_binding/binding-1", in); rec.Code != http.StatusCreated {
		t.Fatalf("admin create status = %d, body %s", rec.Code, rec.Body)
	}
	// Even the creator (stamped as owner) cannot read it back without admin.
	if rec := doAs(t, mux, "root", nil, http.MethodGet, "/api/v1/iam_binding/binding-1", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin get status = %d, want 403 even for the stamped owner", rec.Code)
	}
	if rec := doAs(t, mux, "anyone", []string{"admin"}, http.MethodGet, "/api/v1/iam_binding/binding-1", nil); rec.Code != http.StatusOK {
		t.Fatalf("admin get status = %d", rec.Code)
	}
}
