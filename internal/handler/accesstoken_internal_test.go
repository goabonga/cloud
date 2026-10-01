// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/accesstoken"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/identity"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// TestAccessTokenHandlerPutRejectsMissingIdentity exercises put's own
// "no identity" branch directly, in-package. Register always wraps put
// behind requireAdmin, which never lets an unauthenticated request reach it,
// so going through the registered mux can never observe this branch; calling
// the unexported method directly is the only way to cover it.
func TestAccessTokenHandlerPutRejectsMissingIdentity(t *testing.T) {
	store := state.NewFileStore(t.TempDir())
	users := identity.NewService(registry.New[resource.UserSpec, resource.UserStatus](store, resource.KindUser))
	reg := registry.New[resource.AccessTokenSpec, resource.AccessTokenStatus](store, resource.KindAccessToken)
	h := NewAccessTokenHandler(accesstoken.NewService(reg, users))

	req := httptest.NewRequest(http.MethodPut, "/access_token/t-1", strings.NewReader(`{"spec":{"name":"ci"}}`))
	req.SetPathValue("uid", "t-1")
	rec := httptest.NewRecorder()
	h.put(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
