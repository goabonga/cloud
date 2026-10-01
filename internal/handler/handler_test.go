// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/handler"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func newMux(t *testing.T) (*http.ServeMux, *registry.Registry[resource.VPCSpec, resource.VPCStatus]) {
	t.Helper()
	reg := registry.New[resource.VPCSpec, resource.VPCStatus](state.NewFileStore(t.TempDir()), resource.KindVPC)
	mux := http.NewServeMux()
	handler.New(reg, resource.KindVPC).Register(mux, "/api/v1")
	return mux, reg
}

func do(t *testing.T, mux *http.ServeMux, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHandlerCreateGetUpdateDelete(t *testing.T) {
	t.Parallel()

	mux, _ := newMux(t)

	// Empty list.
	rec := do(t, mux, http.MethodGet, "/api/v1/vpc", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var list resource.List[resource.VPCSpec, resource.VPCStatus]
	mustDecode(t, rec, &list)
	if len(list.Items) != 0 {
		t.Fatalf("expected empty list, got %d", len(list.Items))
	}

	// Create.
	in := resource.VPC{Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	rec = do(t, mux, http.MethodPut, "/api/v1/vpc/vpc-1", in)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body %s", rec.Code, rec.Body)
	}
	var created resource.VPC
	mustDecode(t, rec, &created)
	if created.Metadata.UID != "vpc-1" || created.Metadata.Generation != 1 {
		t.Fatalf("unexpected created meta: %+v", created.Metadata)
	}
	if created.Metadata.CreatedAt.IsZero() {
		t.Fatal("createdAt should be set")
	}

	// Get.
	rec = do(t, mux, http.MethodGet, "/api/v1/vpc/vpc-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d", rec.Code)
	}

	// Update bumps generation and is a 200, not 201.
	in.Spec.CIDR = "10.1.0.0/16"
	rec = do(t, mux, http.MethodPut, "/api/v1/vpc/vpc-1", in)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d", rec.Code)
	}
	var updated resource.VPC
	mustDecode(t, rec, &updated)
	if updated.Metadata.Generation != 2 {
		t.Fatalf("generation = %d, want 2", updated.Metadata.Generation)
	}

	// Delete then 404.
	rec = do(t, mux, http.MethodDelete, "/api/v1/vpc/vpc-1", nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}
	rec = do(t, mux, http.MethodGet, "/api/v1/vpc/vpc-1", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get-after-delete status = %d", rec.Code)
	}
}

func TestHandlerPreservesStatusOnUpdate(t *testing.T) {
	t.Parallel()

	mux, reg := newMux(t)

	in := resource.VPC{Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	if rec := do(t, mux, http.MethodPut, "/api/v1/vpc/vpc-1", in); rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d", rec.Code)
	}

	// Simulate a controller writing status out of band.
	stored, err := reg.Get("vpc-1")
	if err != nil {
		t.Fatalf("get stored: %v", err)
	}
	stored.Status.BridgeName = "br-vpc-1"
	stored.Status.SetPhase(resource.PhaseReady, "Created", "ok")
	if err := reg.Put(stored); err != nil {
		t.Fatalf("put stored: %v", err)
	}

	// A client spec update must not clobber the controller-owned status.
	in.Spec.CIDR = "10.2.0.0/16"
	if rec := do(t, mux, http.MethodPut, "/api/v1/vpc/vpc-1", in); rec.Code != http.StatusOK {
		t.Fatalf("update status = %d", rec.Code)
	}
	out, err := reg.Get("vpc-1")
	if err != nil {
		t.Fatalf("get final: %v", err)
	}
	if out.Status.BridgeName != "br-vpc-1" || !out.Status.IsReady() {
		t.Fatalf("status not preserved: %+v", out.Status)
	}
	if out.Spec.CIDR != "10.2.0.0/16" {
		t.Fatalf("spec not updated: %q", out.Spec.CIDR)
	}
}

func TestHandlerRejectsBadInput(t *testing.T) {
	t.Parallel()

	mux, _ := newMux(t)

	// Invalid CIDR fails validation.
	rec := do(t, mux, http.MethodPut, "/api/v1/vpc/vpc-1", resource.VPC{Spec: resource.VPCSpec{CIDR: "nope"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad cidr status = %d", rec.Code)
	}

	// Unknown fields are rejected by the strict decoder.
	req := httptest.NewRequest(http.MethodPut, "/api/v1/vpc/vpc-1", bytes.NewBufferString(`{"bogus": true}`))
	r2 := httptest.NewRecorder()
	mux.ServeHTTP(r2, req)
	if r2.Code != http.StatusBadRequest {
		t.Fatalf("unknown-field status = %d", r2.Code)
	}
}

func TestHandlerStoresTheSpecWithItsDefaults(t *testing.T) {
	t.Parallel()

	reg := registry.New[resource.LBTargetSpec, resource.LBTargetStatus](state.NewFileStore(t.TempDir()), resource.KindLBTarget)
	mux := http.NewServeMux()
	handler.New(reg, resource.KindLBTarget).Register(mux, "/api/v1")

	rec := do(t, mux, http.MethodPut, "/api/v1/lb_target/t-1", resource.LBTarget{Spec: resource.LBTargetSpec{TargetGroupID: "tg-1", ComputeID: "i-1"}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body %s", rec.Code, rec.Body)
	}
	var created resource.LBTarget
	mustDecode(t, rec, &created)
	if created.Spec.Weight != 1 {
		t.Fatalf("response weight = %d, want the default 1", created.Spec.Weight)
	}
	stored, err := reg.Get("t-1")
	if err != nil || stored.Spec.Weight != 1 {
		t.Fatalf("stored weight = %+v, %v, want the default 1", stored, err)
	}
}

func TestHandlerSoftDeleteWithFinalizer(t *testing.T) {
	t.Parallel()

	mux, reg := newMux(t)

	// Seed a resource that already carries a finalizer (as the agent would add).
	seed := &resource.VPC{
		Metadata: resource.ObjectMeta{UID: "vpc-1", Finalizers: []string{resource.VPCFinalizer}},
		Spec:     resource.VPCSpec{CIDR: "10.0.0.0/16"},
	}
	if err := reg.Put(seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rec := do(t, mux, http.MethodDelete, "/api/v1/vpc/vpc-1", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete status = %d, want 202", rec.Code)
	}

	// The record stays until the finalizer is cleared, now marked for deletion.
	got, err := reg.Get("vpc-1")
	if err != nil {
		t.Fatalf("get after soft delete: %v", err)
	}
	if !got.Metadata.IsDeleting() {
		t.Fatal("expected DeletionTimestamp to be set")
	}
}

func mustDecode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
