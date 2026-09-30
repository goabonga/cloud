// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/goabonga/infrastructure/internal/auth"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/identity"
	"github.com/goabonga/infrastructure/internal/state"
)

// AdminRole is the role required to manage other users' accounts.
const AdminRole = "admin"

// UserHandler serves users over HTTP. Unlike the generic handler it never
// returns password material, and every route but a user's own GET requires
// the admin role.
type UserHandler struct {
	svc *identity.Service
}

// NewUserHandler returns a handler backed by svc.
func NewUserHandler(svc *identity.Service) *UserHandler {
	return &UserHandler{svc: svc}
}

// Register mounts the user routes under base (e.g. "/users"). The caller is
// expected to have already authenticated the request (see auth.Middleware);
// these routes additionally require the admin role, except that a caller may
// always GET their own record.
func (h *UserHandler) Register(mux *http.ServeMux, base string) {
	p := base + "/" + resource.KindUser
	mux.HandleFunc("GET "+p, h.requireAdmin(h.list))
	mux.HandleFunc("GET "+p+"/{uid}", h.get)
	mux.HandleFunc("PUT "+p+"/{uid}", h.requireAdmin(h.put))
	mux.HandleFunc("DELETE "+p+"/{uid}", h.requireAdmin(h.delete))
}

func (h *UserHandler) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.IdentityFrom(r.Context())
		if !ok || !id.HasRole(AdminRole) {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}
		next(w, r)
	}
}

func (h *UserHandler) list(w http.ResponseWriter, _ *http.Request) {
	items, err := h.svc.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resource.List[resource.UserSpec, resource.UserStatus]{
		APIVersion: resource.APIVersion,
		Kind:       resource.KindUser,
		Items:      items,
	})
}

func (h *UserHandler) get(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	id, ok := auth.IdentityFrom(r.Context())
	if !ok || (id.Subject != uid && !id.HasRole(AdminRole)) {
		writeError(w, http.StatusForbidden, "admin role required")
		return
	}
	usr, err := h.svc.Get(uid)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, usr)
	case errors.Is(err, state.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (h *UserHandler) put(w http.ResponseWriter, r *http.Request) {
	var in resource.User
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	out, err := h.svc.Put(r.PathValue("uid"), in.Spec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *UserHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.PathValue("uid")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
