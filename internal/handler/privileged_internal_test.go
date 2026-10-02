// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func TestPrivilegedComputeRequiresAdmin(t *testing.T) {
	reg := registry.New[resource.ComputeSpec, resource.ComputeStatus](state.NewFileStore(t.TempDir()), resource.KindCompute)
	mux := http.NewServeMux()
	New(reg, resource.KindCompute).Register(mux, "/api/v1")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("PUT", "/api/v1/compute/c", strings.NewReader(`{"spec":{"subnetId":"s","image":"i","privileged":true}}`)))
	if w.Code != http.StatusForbidden {
		t.Fatalf("privileged without admin: %d", w.Code)
	}
}
