// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package httpsrv_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/auth"
	"github.com/goabonga/infrastructure/internal/crypto"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/httpsrv"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// roleAwareAuthenticator is a bearer-token fake carrying roles, which
// auth.TokenAuthenticator does not support.
type roleAwareAuthenticator map[string]auth.Identity

func (a roleAwareAuthenticator) Authenticate(r *http.Request) (*auth.Identity, error) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return nil, auth.ErrUnauthenticated
	}
	id, ok := a[tok]
	if !ok {
		return nil, auth.ErrUnauthenticated
	}
	return &id, nil
}

func TestServerHealthz(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(httpsrv.New(state.NewFileStore(t.TempDir())).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("get healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d", resp.StatusCode)
	}
}

func TestServerWiresVPCRoutes(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(httpsrv.New(state.NewFileStore(t.TempDir())).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/vpc")
	if err != nil {
		t.Fatalf("get vpc collection: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("vpc list status = %d", resp.StatusCode)
	}
}

func TestServerAuthGatesAPIButNotHealth(t *testing.T) {
	t.Parallel()

	tokenAuth := auth.NewTokenAuthenticator(map[string]string{"tok": "alice"})
	srv := httptest.NewServer(httpsrv.New(state.NewFileStore(t.TempDir()), httpsrv.WithAuth(tokenAuth)).Handler())
	defer srv.Close()

	// Health is open.
	health, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	_ = health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d", health.StatusCode)
	}

	// API without a token is rejected.
	unauth, err := http.Get(srv.URL + "/api/v1/vpc")
	if err != nil {
		t.Fatalf("vpc no-auth: %v", err)
	}
	_ = unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-auth status = %d, want 401", unauth.StatusCode)
	}

	// API with the token succeeds.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/vpc", nil)
	req.Header.Set("Authorization", "Bearer tok")
	authed, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("vpc auth: %v", err)
	}
	_ = authed.Body.Close()
	if authed.StatusCode != http.StatusOK {
		t.Fatalf("auth status = %d, want 200", authed.StatusCode)
	}
}

func TestServerWiresACLRoutes(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(httpsrv.New(state.NewFileStore(t.TempDir())).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/acl_policy")
	if err != nil {
		t.Fatalf("get acl_policy collection: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("acl_policy list status = %d", resp.StatusCode)
	}
}

func TestServerWiresListenerRoutes(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(httpsrv.New(state.NewFileStore(t.TempDir())).Handler())
	defer srv.Close()

	for _, kind := range []string{"lb_target_group", "lb_target", "lb_listener"} {
		resp, err := http.Get(srv.URL + "/api/v1/" + kind)
		if err != nil {
			t.Fatalf("get %s collection: %v", kind, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s list status = %d", kind, resp.StatusCode)
		}
	}
}

func TestServerEnforcesOwnershipAndSharing(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	tokenAuth := roleAwareAuthenticator{
		"admin-tok": {Subject: "root", Roles: []string{"admin"}},
		"alice-tok": {Subject: "alice"},
		"bob-tok":   {Subject: "bob"},
	}
	srv := httptest.NewServer(httpsrv.New(store, httpsrv.WithAuth(tokenAuth)).Handler())
	defer srv.Close()

	projects := registry.New[resource.ProjectSpec, resource.ProjectStatus](store, resource.KindProject)
	if err := projects.Put(&resource.Project{
		Metadata: resource.ObjectMeta{UID: "project-1"},
		Spec: resource.ProjectSpec{
			DisplayName: "demo",
			ParentRef:   resource.ParentRef{ParentKind: resource.ParentKindOrganization, ParentID: "org-1"},
		},
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	putAs := func(token, path string, body any) int {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		req, err := http.NewRequest(http.MethodPut, srv.URL+path, bytes.NewReader(buf))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do request: %v", err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	getAs := func(token, path string) (int, []byte) {
		req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do request: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, body
	}

	// alice cannot create into project-1 without a grant.
	noGrant := resource.VPC{Metadata: resource.ObjectMeta{ProjectID: "project-1"}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	if code := putAs("alice-tok", "/api/v1/vpc/vpc-1", noGrant); code != http.StatusForbidden {
		t.Fatalf("create without a grant: status = %d, want 403", code)
	}

	// Only admin can grant access: alice herself cannot create the binding
	// that would grant her editor on project-1 (iam_binding is admin-only,
	// precisely so a caller can't self-grant access this way).
	binding := resource.IAMBinding{Spec: resource.IAMBindingSpec{
		Resource: resource.ObjectReference{Kind: resource.KindProject, UID: "project-1"},
		Role:     resource.RoleEditor,
		Members:  []string{"user:alice"},
	}}
	if code := putAs("alice-tok", "/api/v1/iam_binding/binding-1", binding); code != http.StatusForbidden {
		t.Fatalf("alice self-granting a binding: status = %d, want 403", code)
	}
	if code := putAs("admin-tok", "/api/v1/iam_binding/binding-1", binding); code != http.StatusCreated {
		t.Fatalf("admin granting alice editor: status = %d", code)
	}

	if code := putAs("alice-tok", "/api/v1/vpc/vpc-1", noGrant); code != http.StatusCreated {
		t.Fatalf("create with an editor grant: status = %d, want 201", code)
	}

	// bob has no grant anywhere: invisible.
	if code, _ := getAs("bob-tok", "/api/v1/vpc/vpc-1"); code != http.StatusForbidden {
		t.Fatalf("bob get before sharing: status = %d, want 403", code)
	}
	if code, body := getAs("bob-tok", "/api/v1/vpc"); code != http.StatusOK || bytes.Contains(body, []byte("vpc-1")) {
		t.Fatalf("bob list before sharing: status = %d, body %s, want vpc-1 absent", code, body)
	}

	// Admin shares project-1 with bob as a viewer.
	shareWithBob := resource.IAMBinding{Spec: resource.IAMBindingSpec{
		Resource: resource.ObjectReference{Kind: resource.KindProject, UID: "project-1"},
		Role:     resource.RoleViewer,
		Members:  []string{"user:bob"},
	}}
	if code := putAs("admin-tok", "/api/v1/iam_binding/binding-2", shareWithBob); code != http.StatusCreated {
		t.Fatalf("admin sharing with bob: status = %d, want 201", code)
	}

	if code, _ := getAs("bob-tok", "/api/v1/vpc/vpc-1"); code != http.StatusOK {
		t.Fatalf("bob get after sharing: status = %d, want 200", code)
	}
	if code, body := getAs("bob-tok", "/api/v1/vpc"); code != http.StatusOK || !bytes.Contains(body, []byte("vpc-1")) {
		t.Fatalf("bob list after sharing: status = %d, body %s, want vpc-1 present", code, body)
	}
}

func TestServerWiresHierarchyRoutes(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(httpsrv.New(state.NewFileStore(t.TempDir())).Handler())
	defer srv.Close()

	for _, kind := range []string{"organization", "folder", "project", "iam_binding"} {
		resp, err := http.Get(srv.URL + "/api/v1/" + kind)
		if err != nil {
			t.Fatalf("get %s collection: %v", kind, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s list status = %d", kind, resp.StatusCode)
		}
	}
}

func TestServerSecretRoutesDisabledByDefault(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(httpsrv.New(state.NewFileStore(t.TempDir())).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/secret")
	if err != nil {
		t.Fatalf("get secret collection: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("secret route should be absent without a KEK, got %d", resp.StatusCode)
	}
}

func TestServerSecretRoutesEnabledWithKEK(t *testing.T) {
	t.Parallel()

	key, _ := crypto.GenerateKey()
	kek, err := crypto.NewKEK(key)
	if err != nil {
		t.Fatalf("kek: %v", err)
	}
	srv := httptest.NewServer(httpsrv.New(state.NewFileStore(t.TempDir()), httpsrv.WithSecretEncryption(kek)).Handler())
	defer srv.Close()

	put, err := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/secret/sec-1",
		bytes.NewBufferString(`{"spec":{"data":"top-secret"}}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatalf("put secret: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put secret status = %d", resp.StatusCode)
	}

	// The SSL CA routes are wired by the same KEK option.
	ca, err := http.Get(srv.URL + "/api/v1/ssl_ca")
	if err != nil {
		t.Fatalf("get ssl_ca: %v", err)
	}
	defer func() { _ = ca.Body.Close() }()
	if ca.StatusCode != http.StatusOK {
		t.Fatalf("ssl_ca list status = %d", ca.StatusCode)
	}
}
