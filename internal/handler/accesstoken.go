// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/goabonga/infrastructure/internal/accesstoken"
	"github.com/goabonga/infrastructure/internal/auth"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/state"
)

// AccessTokenHandler serves access tokens over HTTP. Every route requires the
// admin role: an access token always acts as its creator, so managing anyone
// else's is managing their own credentials by construction.
type AccessTokenHandler struct {
	svc *accesstoken.Service
}

// NewAccessTokenHandler returns a handler backed by svc.
func NewAccessTokenHandler(svc *accesstoken.Service) *AccessTokenHandler {
	return &AccessTokenHandler{svc: svc}
}

// Register mounts the access token routes under base (e.g. "" for the idp's
// own root mux).
func (h *AccessTokenHandler) Register(mux *http.ServeMux, base string) {
	p := base + "/" + resource.KindAccessToken
	mux.HandleFunc("GET "+p, h.requireAdmin(h.list))
	mux.HandleFunc("GET "+p+"/{uid}", h.requireAdmin(h.get))
	mux.HandleFunc("PUT "+p+"/{uid}", h.requireAdmin(h.put))
	mux.HandleFunc("DELETE "+p+"/{uid}", h.requireAdmin(h.delete))
}

func (h *AccessTokenHandler) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.IdentityFrom(r.Context())
		if !ok || !id.HasRole(AdminRole) {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}
		next(w, r)
	}
}

func (h *AccessTokenHandler) list(w http.ResponseWriter, _ *http.Request) {
	items, err := h.svc.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resource.List[resource.AccessTokenSpec, resource.AccessTokenStatus]{
		APIVersion: resource.APIVersion,
		Kind:       resource.KindAccessToken,
		Items:      items,
	})
}

func (h *AccessTokenHandler) get(w http.ResponseWriter, r *http.Request) {
	at, err := h.svc.Get(r.PathValue("uid"))
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, at)
	case errors.Is(err, state.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// accessTokenResponse is the AccessToken envelope plus the plaintext token,
// present only on the response that generated it - never persisted, never
// returned again.
type accessTokenResponse struct {
	resource.AccessToken
	Token string `json:"token,omitempty"`
}

func (h *AccessTokenHandler) put(w http.ResponseWriter, r *http.Request) {
	var in resource.AccessToken
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	id, ok := auth.IdentityFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid token")
		return
	}
	out, plaintext, err := h.svc.Put(r.PathValue("uid"), id.Subject, in.Spec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, accessTokenResponse{AccessToken: *out, Token: plaintext})
}

func (h *AccessTokenHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.PathValue("uid")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
