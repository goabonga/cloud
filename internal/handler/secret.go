// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/secret"
	"github.com/goabonga/infrastructure/internal/state"
)

// SecretHandler serves secrets over HTTP. Unlike the generic handler it never
// returns secret material on list/get; plaintext is only available through the
// explicit reveal endpoint.
type SecretHandler struct {
	svc *secret.Service
}

// NewSecretHandler returns a handler backed by svc.
func NewSecretHandler(svc *secret.Service) *SecretHandler {
	return &SecretHandler{svc: svc}
}

// Register mounts the secret routes under base (e.g. "/api/v1").
func (h *SecretHandler) Register(mux *http.ServeMux, base string) {
	p := base + "/" + resource.KindSecret
	mux.HandleFunc("GET "+p, h.list)
	mux.HandleFunc("GET "+p+"/{uid}", h.get)
	mux.HandleFunc("GET "+p+"/{uid}/reveal", h.reveal)
	mux.HandleFunc("PUT "+p+"/{uid}", h.put)
	mux.HandleFunc("DELETE "+p+"/{uid}", h.delete)

	v := base + "/" + resource.KindSecretVersion
	mux.HandleFunc("GET "+v, h.listVersions)
	mux.HandleFunc("GET "+v+"/{uid}", h.getVersion)
	mux.HandleFunc("GET "+v+"/{uid}/reveal", h.revealVersion)
	mux.HandleFunc("PUT "+v+"/{uid}", h.putVersion)
	mux.HandleFunc("DELETE "+v+"/{uid}", h.deleteVersion)
}

func (h *SecretHandler) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeList(w, r, resource.KindSecret, items)
}

func (h *SecretHandler) get(w http.ResponseWriter, r *http.Request) {
	sec, err := h.svc.Get(r.PathValue("uid"))
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, sec)
	case errors.Is(err, state.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (h *SecretHandler) reveal(w http.ResponseWriter, r *http.Request) {
	plaintext, err := h.svc.Reveal(r.PathValue("uid"))
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"data": plaintext})
	case errors.Is(err, state.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (h *SecretHandler) put(w http.ResponseWriter, r *http.Request) {
	var in resource.Secret
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if in.Spec.Data == "" {
		writeError(w, http.StatusBadRequest, "secret: data is required")
		return
	}
	out, err := h.svc.Put(r.PathValue("uid"), in.Metadata.Name, in.Spec.Data)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *SecretHandler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.PathValue("uid")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *SecretHandler) listVersions(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListVersions(r.URL.Query().Get("secretId"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeList(w, r, resource.KindSecretVersion, items)
}

func (h *SecretHandler) getVersion(w http.ResponseWriter, r *http.Request) {
	ver, err := h.svc.GetVersion(r.PathValue("uid"))
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, ver)
	case errors.Is(err, state.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (h *SecretHandler) revealVersion(w http.ResponseWriter, r *http.Request) {
	plaintext, err := h.svc.RevealVersion(r.PathValue("uid"))
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"data": plaintext})
	case errors.Is(err, state.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (h *SecretHandler) putVersion(w http.ResponseWriter, r *http.Request) {
	var in resource.SecretVersion
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if err := in.Spec.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := h.svc.AddVersion(r.PathValue("uid"), in.Spec.SecretID, in.Spec.Data)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *SecretHandler) deleteVersion(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DeleteVersion(r.PathValue("uid")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
