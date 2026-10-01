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

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// Handler serves CRUD requests for one resource kind. S is the spec type, ST
// the status type.
type Handler[S any, ST any] struct {
	reg  *registry.Registry[S, ST]
	kind string
	now  func() time.Time
}

// New returns a Handler backed by reg for the given kind.
func New[S any, ST any](reg *registry.Registry[S, ST], kind string) *Handler[S, ST] {
	return &Handler[S, ST]{reg: reg, kind: kind, now: time.Now}
}

// Register mounts the collection and item routes under base (e.g. "/api/v1").
func (h *Handler[S, ST]) Register(mux *http.ServeMux, base string) {
	prefix := base + "/" + h.kind
	mux.HandleFunc("GET "+prefix, h.list)
	mux.HandleFunc("GET "+prefix+"/{uid}", h.get)
	mux.HandleFunc("PUT "+prefix+"/{uid}", h.put)
	mux.HandleFunc("DELETE "+prefix+"/{uid}", h.delete)
}

func (h *Handler[S, ST]) list(w http.ResponseWriter, _ *http.Request) {
	items, err := h.reg.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
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

	existing, err := h.reg.Get(uid)
	created := false
	switch {
	case err == nil:
		// Spec is client-owned; status, creation time and generation are not.
		res.Metadata.CreatedAt = existing.Metadata.CreatedAt
		res.Metadata.Generation = existing.Metadata.Generation + 1
		res.Status = existing.Status
	case errors.Is(err, state.ErrNotFound):
		var zero ST
		res.Metadata.CreatedAt = h.now()
		res.Metadata.Generation = 1
		res.Status = zero
		created = true
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
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
