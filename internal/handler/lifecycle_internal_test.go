// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func TestPutPreservesServerLifecycle(t *testing.T) {
	reg := registry.New[resource.DiskSpec, resource.DiskStatus](state.NewFileStore(t.TempDir()), resource.KindDisk)
	mux := http.NewServeMux()
	New(reg, resource.KindDisk).Register(mux, "/api/v1")
	now := time.Now().UTC()
	send := func(in resource.Disk) {
		t.Helper()
		body, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/disk/d1", bytes.NewReader(body)))
		if w.Code != http.StatusOK && w.Code != http.StatusCreated {
			t.Fatalf("put: %d %s", w.Code, w.Body)
		}
	}
	send(resource.Disk{Metadata: resource.ObjectMeta{Finalizers: []string{"client"}, DeletionTimestamp: &now}, Spec: resource.DiskSpec{SizeMB: 32}})
	got, err := reg.Get("d1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata.IsDeleting() || len(got.Metadata.Finalizers) != 0 {
		t.Fatal("client controlled lifecycle on create")
	}
	got.Metadata.Finalizers = []string{resource.DiskFinalizer}
	got.Metadata.DeletionTimestamp = &now
	if err := reg.Put(got); err != nil {
		t.Fatal(err)
	}
	send(resource.Disk{Spec: resource.DiskSpec{SizeMB: 64}})
	got, err = reg.Get("d1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Metadata.IsDeleting() || !got.Metadata.HasFinalizer(resource.DiskFinalizer) {
		t.Fatal("update removed server lifecycle")
	}
}
