// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package registry_test

import (
	"errors"
	"sort"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

type vpcSpec struct {
	CIDR string `json:"cidr"`
}

type vpcStatus struct {
	resource.StatusBase
	AssignedCIDR string `json:"assignedCidr"`
}

func newRegistry(t *testing.T) *registry.Registry[vpcSpec, vpcStatus] {
	t.Helper()
	return registry.New[vpcSpec, vpcStatus](state.NewFileStore(t.TempDir()), "vpc")
}

func TestRegistryPutGetRoundtrip(t *testing.T) {
	t.Parallel()

	r := newRegistry(t)
	in := &resource.Resource[vpcSpec, vpcStatus]{
		Metadata: resource.ObjectMeta{UID: "vpc-1", Name: "prod"},
		Spec:     vpcSpec{CIDR: "10.0.0.0/16"},
	}
	in.Status.SetPhase(resource.PhaseReady, "Created", "ok")

	if err := r.Put(in); err != nil {
		t.Fatalf("put: %v", err)
	}

	out, err := r.Get("vpc-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if out.APIVersion != resource.APIVersion {
		t.Fatalf("APIVersion = %q, want %q", out.APIVersion, resource.APIVersion)
	}
	if out.Kind != "vpc" {
		t.Fatalf("Kind = %q, want vpc", out.Kind)
	}
	if out.Spec.CIDR != "10.0.0.0/16" {
		t.Fatalf("Spec.CIDR = %q", out.Spec.CIDR)
	}
	if !out.Status.IsReady() {
		t.Fatal("expected Ready status to round-trip")
	}
}

func TestRegistryPutValidation(t *testing.T) {
	t.Parallel()

	r := newRegistry(t)
	if err := r.Put(nil); err == nil {
		t.Fatal("expected error for nil resource")
	}
	if err := r.Put(&resource.Resource[vpcSpec, vpcStatus]{}); err == nil {
		t.Fatal("expected error for empty UID")
	}
}

func TestRegistryGetMissing(t *testing.T) {
	t.Parallel()

	r := newRegistry(t)
	if _, err := r.Get("nope"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRegistryList(t *testing.T) {
	t.Parallel()

	r := newRegistry(t)
	for _, uid := range []string{"vpc-1", "vpc-2"} {
		res := &resource.Resource[vpcSpec, vpcStatus]{
			Metadata: resource.ObjectMeta{UID: uid},
			Spec:     vpcSpec{CIDR: "10.0.0.0/16"},
		}
		if err := r.Put(res); err != nil {
			t.Fatalf("put %s: %v", uid, err)
		}
	}

	items, err := r.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	uids := make([]string, 0, len(items))
	for _, it := range items {
		uids = append(uids, it.Metadata.UID)
	}
	sort.Strings(uids)
	if len(uids) != 2 || uids[0] != "vpc-1" || uids[1] != "vpc-2" {
		t.Fatalf("unexpected uids: %v", uids)
	}
}

func TestRegistryTryUpdateApplies(t *testing.T) {
	t.Parallel()

	r := newRegistry(t)
	if err := r.Put(&resource.Resource[vpcSpec, vpcStatus]{
		Metadata: resource.ObjectMeta{UID: "vpc-1"},
		Spec:     vpcSpec{CIDR: "10.0.0.0/16"},
	}); err != nil {
		t.Fatalf("put: %v", err)
	}

	ok, err := r.TryUpdate("vpc-1", func(res *resource.Resource[vpcSpec, vpcStatus]) error {
		res.Status.AssignedCIDR = "10.0.0.1/32"
		return nil
	})
	if err != nil {
		t.Fatalf("tryupdate: %v", err)
	}
	if !ok {
		t.Fatal("want the update applied with no concurrent writer")
	}

	out, err := r.Get("vpc-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if out.Status.AssignedCIDR != "10.0.0.1/32" {
		t.Fatalf("AssignedCIDR = %q, want 10.0.0.1/32", out.Status.AssignedCIDR)
	}
}

func TestRegistryTryUpdateLosesRaceOnStaleRead(t *testing.T) {
	t.Parallel()

	r := newRegistry(t)
	if err := r.Put(&resource.Resource[vpcSpec, vpcStatus]{
		Metadata: resource.ObjectMeta{UID: "vpc-1"},
		Spec:     vpcSpec{CIDR: "10.0.0.0/16"},
	}); err != nil {
		t.Fatalf("put: %v", err)
	}

	// A concurrent writer changes the stored value between TryUpdate's read
	// and its compare-and-swap by mutating inside the callback itself.
	first := true
	ok, err := r.TryUpdate("vpc-1", func(res *resource.Resource[vpcSpec, vpcStatus]) error {
		if first {
			first = false
			if err := r.Put(&resource.Resource[vpcSpec, vpcStatus]{
				Metadata: resource.ObjectMeta{UID: "vpc-1"},
				Spec:     vpcSpec{CIDR: "10.0.0.0/16"},
				Status:   vpcStatus{AssignedCIDR: "raced-in-first"},
			}); err != nil {
				t.Fatalf("concurrent put: %v", err)
			}
		}
		res.Status.AssignedCIDR = "lost-the-race"
		return nil
	})
	if err != nil {
		t.Fatalf("tryupdate: %v", err)
	}
	if ok {
		t.Fatal("want the update to lose the race against the concurrent write")
	}

	out, err := r.Get("vpc-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if out.Status.AssignedCIDR != "raced-in-first" {
		t.Fatalf("AssignedCIDR = %q, want the concurrent writer's value to win", out.Status.AssignedCIDR)
	}
}

func TestRegistryTryUpdatePropagatesMutateError(t *testing.T) {
	t.Parallel()

	r := newRegistry(t)
	if err := r.Put(&resource.Resource[vpcSpec, vpcStatus]{
		Metadata: resource.ObjectMeta{UID: "vpc-1"},
	}); err != nil {
		t.Fatalf("put: %v", err)
	}

	wantErr := errors.New("boom")
	_, err := r.TryUpdate("vpc-1", func(*resource.Resource[vpcSpec, vpcStatus]) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
}

func TestRegistryDelete(t *testing.T) {
	t.Parallel()

	r := newRegistry(t)
	res := &resource.Resource[vpcSpec, vpcStatus]{
		Metadata: resource.ObjectMeta{UID: "vpc-1"},
	}
	if err := r.Put(res); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := r.Delete("vpc-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := r.Get("vpc-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
