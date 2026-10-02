// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/ssl"
	"github.com/goabonga/infrastructure/internal/state"
)

// SSLHandler serves certificate authorities and certificate issuance. CA
// private keys are never returned; the issue endpoint mints a fresh leaf
// certificate and key signed by the CA.
type SSLHandler struct {
	svc *ssl.Service
}

// NewSSLHandler returns a handler backed by svc.
func NewSSLHandler(svc *ssl.Service) *SSLHandler {
	return &SSLHandler{svc: svc}
}

// Register mounts the CA routes under base (e.g. "/api/v1").
func (h *SSLHandler) Register(mux *http.ServeMux, base string) {
	p := base + "/" + resource.KindSSLCA
	mux.HandleFunc("GET "+p, h.list)
	mux.HandleFunc("GET "+p+"/{uid}", h.get)
	mux.HandleFunc("PUT "+p+"/{uid}", h.put)
	mux.HandleFunc("DELETE "+p+"/{uid}", h.delete)
	mux.HandleFunc("POST "+p+"/{uid}/issue", h.issue)

	c := base + "/" + resource.KindSSLCert
	mux.HandleFunc("GET "+c, h.listCerts)
	mux.HandleFunc("GET "+c+"/{uid}", h.getCert)
	mux.HandleFunc("GET "+c+"/{uid}/reveal", h.revealCert)
	mux.HandleFunc("PUT "+c+"/{uid}", h.putCert)
	mux.HandleFunc("DELETE "+c+"/{uid}", h.deleteCert)
}

func (h *SSLHandler) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeList(w, r, resource.KindSSLCA, items)
}

func (h *SSLHandler) get(w http.ResponseWriter, r *http.Request) {
	ca, err := h.svc.Get(r.PathValue("uid"))
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, ca)
	case errors.Is(err, state.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (h *SSLHandler) put(w http.ResponseWriter, r *http.Request) {
	var in resource.SSLCA
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
	// The global root belongs to the platform: no client creates, replaces or
	// widens it, nor marks its own CA trusted everywhere.
	if in.Spec.Global || r.PathValue("uid") == ssl.GlobalRootUID {
		writeError(w, http.StatusForbidden, "the global root CA is managed by the platform")
		return
	}
	out, err := h.svc.CreateCA(r.PathValue("uid"), in.Metadata.Name, in.Spec)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *SSLHandler) delete(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("uid") == ssl.GlobalRootUID {
		writeError(w, http.StatusForbidden, "the global root CA is managed by the platform")
		return
	}
	if err := h.svc.Delete(r.PathValue("uid")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type issueRequest struct {
	CommonName string   `json:"commonName"`
	DNSNames   []string `json:"dnsNames"`
	ValidDays  int      `json:"validDays"`
}

func (h *SSLHandler) issue(w http.ResponseWriter, r *http.Request) {
	var req issueRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
		return
	}
	if req.CommonName == "" {
		writeError(w, http.StatusBadRequest, "commonName is required")
		return
	}
	certPEM, keyPEM, err := h.svc.Issue(r.PathValue("uid"), req.CommonName, req.DNSNames, req.ValidDays)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"cert": string(certPEM), "key": string(keyPEM)})
	case errors.Is(err, state.ErrNotFound):
		writeError(w, http.StatusNotFound, "ca not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (h *SSLHandler) listCerts(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.ListCert()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeList(w, r, resource.KindSSLCert, items)
}

func (h *SSLHandler) getCert(w http.ResponseWriter, r *http.Request) {
	cert, err := h.svc.GetCert(r.PathValue("uid"))
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, cert)
	case errors.Is(err, state.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (h *SSLHandler) revealCert(w http.ResponseWriter, r *http.Request) {
	certPEM, keyPEM, err := h.svc.RevealCert(r.PathValue("uid"))
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"cert": string(certPEM), "key": string(keyPEM)})
	case errors.Is(err, state.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (h *SSLHandler) putCert(w http.ResponseWriter, r *http.Request) {
	var in resource.SSLCert
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
	out, err := h.svc.CreateCert(r.PathValue("uid"), in.Metadata.Name, in.Spec)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, out)
	case errors.Is(err, state.ErrNotFound):
		writeError(w, http.StatusNotFound, "ca not found")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (h *SSLHandler) deleteCert(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DeleteCert(r.PathValue("uid")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
