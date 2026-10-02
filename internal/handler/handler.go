// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package handler exposes a resource registry over HTTP. A single generic
// Handler serves the CRUD verbs for one resource kind, so new kinds are wired
// by instantiating it with their spec and status types.
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/goabonga/infrastructure/internal/auth"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/iam"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// Handler serves CRUD requests for one resource kind. S is the spec type, ST
// the status type.
type Handler[S any, ST any] struct {
	reg       *registry.Registry[S, ST]
	kind      string
	now       func() time.Time
	az        *iam.Authorizer
	adminOnly bool
}

// Option configures a Handler.
type Option[S any, ST any] func(*Handler[S, ST])

// WithAuthorization makes the handler consult az before serving a request.
// Without it every request is served regardless of the caller's identity,
// exactly as before this option existed.
func WithAuthorization[S any, ST any](az *iam.Authorizer) Option[S, ST] {
	return func(h *Handler[S, ST]) { h.az = az }
}

// WithAdminOnly restricts every verb to the global admin role, ignoring
// ownership and project grants entirely. Use it for a kind where granting
// self-service access via the owner fast path would itself be a privilege
// escalation - iam_binding is the reason this exists: nothing populates its
// own metadata.projectId, so without this option it would be an unscoped
// resource anyone could create, including one naming themselves as owner of
// a project they otherwise have no access to.
func WithAdminOnly[S any, ST any]() Option[S, ST] {
	return func(h *Handler[S, ST]) { h.adminOnly = true }
}

// New returns a Handler backed by reg for the given kind.
func New[S any, ST any](reg *registry.Registry[S, ST], kind string, opts ...Option[S, ST]) *Handler[S, ST] {
	h := &Handler[S, ST]{reg: reg, kind: kind, now: time.Now}
	for _, o := range opts {
		o(h)
	}
	return h
}

// authorize reports whether the caller may exercise perm on a resource owned
// by ownerUID and scoped to projectID. With neither WithAuthorization nor
// WithAdminOnly configured, every request is allowed. An absent identity or
// a resolution error both count as "not allowed", never as a server error.
func (h *Handler[S, ST]) authorize(r *http.Request, ownerUID, projectID string, perm iam.Permission) bool {
	if !h.adminOnly && h.az == nil {
		return true
	}
	id, ok := auth.IdentityFrom(r.Context())
	if !ok {
		return false
	}
	if h.adminOnly {
		return id.HasRole(AdminRole)
	}
	allowed, err := h.az.Allowed(id.Subject, id.HasRole(AdminRole), ownerUID, projectID, perm)
	return err == nil && allowed
}

// Register mounts the collection and item routes under base (e.g. "/api/v1").
func (h *Handler[S, ST]) Register(mux *http.ServeMux, base string) {
	prefix := base + "/" + h.kind
	mux.HandleFunc("GET "+prefix, h.list)
	mux.HandleFunc("GET "+prefix+"/{uid}", h.get)
	mux.HandleFunc("PUT "+prefix+"/{uid}", h.put)
	mux.HandleFunc("DELETE "+prefix+"/{uid}", h.delete)
}

func (h *Handler[S, ST]) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.reg.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.az != nil || h.adminOnly {
		visible := make([]resource.Resource[S, ST], 0, len(items))
		for _, it := range items {
			if h.authorize(r, it.Metadata.OwnerUID, it.Metadata.ProjectID, iam.PermissionRead) {
				visible = append(visible, it)
			}
		}
		items = visible
	}
	writeJSON(w, http.StatusOK, resource.List[S, ST]{
		APIVersion: resource.APIVersion,
		Kind:       h.kind,
		Items:      items,
	})
}

func (h *Handler[S, ST]) get(w http.ResponseWriter, r *http.Request) {
	res, err := h.reg.Get(r.PathValue("uid"))
	switch {
	case err == nil:
		if !h.authorize(r, res.Metadata.OwnerUID, res.Metadata.ProjectID, iam.PermissionRead) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		writeJSON(w, http.StatusOK, res)
	case errors.Is(err, state.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (h *Handler[S, ST]) put(w http.ResponseWriter, r *http.Request) {
	var res resource.Resource[S, ST]
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&res); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	uid := r.PathValue("uid")
	res.Metadata.UID = uid

	existing, err := h.reg.Get(uid)
	created := false
	switch {
	case err == nil:
		// Metadata.ProjectID is immutable once set - moving a resource between
		// projects is not supported.
		if res.Metadata.ProjectID != existing.Metadata.ProjectID {
			writeError(w, http.StatusBadRequest, "projectId is immutable once set")
			return
		}
		if !h.authorize(r, existing.Metadata.OwnerUID, existing.Metadata.ProjectID, iam.PermissionWrite) {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		// Spec is client-owned; status, creation time, generation and
		// ownership are not.
		res.Metadata.CreatedAt = existing.Metadata.CreatedAt
		res.Metadata.Generation = existing.Metadata.Generation + 1
		res.Metadata.OwnerUID = existing.Metadata.OwnerUID
		res.Metadata.Finalizers = existing.Metadata.Finalizers
		res.Metadata.DeletionTimestamp = existing.Metadata.DeletionTimestamp
		res.Status = existing.Status
	case errors.Is(err, state.ErrNotFound):
		switch {
		case h.adminOnly:
			id, ok := auth.IdentityFrom(r.Context())
			if !ok || !id.HasRole(AdminRole) {
				writeError(w, http.StatusForbidden, "forbidden")
				return
			}
			res.Metadata.OwnerUID = id.Subject
		case h.az != nil:
			id, ok := auth.IdentityFrom(r.Context())
			if !ok {
				writeError(w, http.StatusForbidden, "forbidden")
				return
			}
			// Creating into a project requires a grant there; an unscoped
			// create is always allowed, and the caller becomes its owner.
			if res.Metadata.ProjectID != "" {
				allowed, aerr := h.az.Allowed(id.Subject, id.HasRole(AdminRole), "", res.Metadata.ProjectID, iam.PermissionWrite)
				if aerr != nil || !allowed {
					writeError(w, http.StatusForbidden, "forbidden")
					return
				}
			}
			res.Metadata.OwnerUID = id.Subject
		}
		var zero ST
		res.Metadata.CreatedAt = h.now()
		res.Metadata.Generation = 1
		res.Metadata.Finalizers = nil
		res.Metadata.DeletionTimestamp = nil
		res.Status = zero
		created = true
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Defaults first, so the stored spec - and what the client reads back -
	// holds the effective values.
	if d, ok := any(res.Spec).(resource.Defaulter[S]); ok {
		res.Spec = d.WithDefaults()
	}
	if v, ok := any(res.Spec).(resource.Validator); ok {
		if err := v.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	if err := h.reg.Put(&res); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, &res)
}

func (h *Handler[S, ST]) delete(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("uid")
	existing, err := h.reg.Get(uid)
	switch {
	case errors.Is(err, state.ErrNotFound):
		w.WriteHeader(http.StatusNoContent) // delete is idempotent
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if !h.authorize(r, existing.Metadata.OwnerUID, existing.Metadata.ProjectID, iam.PermissionDelete) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	// With no finalizers the record can go immediately; otherwise mark it for
	// deletion and let the controllers run their finalizers first.
	if len(existing.Metadata.Finalizers) == 0 {
		if err := h.reg.Delete(uid); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	now := h.now()
	existing.Metadata.DeletionTimestamp = &now
	if err := h.reg.Put(existing); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, existing)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
