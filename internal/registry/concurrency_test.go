// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package registry_test

import (
	"errors"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func TestStaleStatusCannotOverwriteNewSpec(t *testing.T) {
	reg := registry.New[resource.DiskSpec, resource.DiskStatus](state.NewFileStore(t.TempDir()), resource.KindDisk)
	original := &resource.Disk{Metadata: resource.ObjectMeta{UID: "d"}, Spec: resource.DiskSpec{SizeMB: 32}}
	if err := reg.Put(original); err != nil {
		t.Fatal(err)
	}
	stale, err := reg.Get("d")
	if err != nil {
		t.Fatal(err)
	}
	latest, err := reg.Get("d")
	if err != nil {
		t.Fatal(err)
	}
	latest.Spec.SizeMB = 64
	if err := reg.Put(latest); err != nil {
		t.Fatal(err)
	}
	stale.Status.SetPhase(resource.PhaseReady, "Ready", "")
	if err := reg.Put(stale); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale write: %v", err)
	}
	got, err := reg.Get("d")
	if err != nil {
		t.Fatal(err)
	}
	if got.Spec.SizeMB != 64 {
		t.Fatal("spec overwritten")
	}
	if err := reg.Put(&resource.Disk{Metadata: resource.ObjectMeta{UID: "d"}}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate create: %v", err)
	}
}
