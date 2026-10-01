// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package handler

import (
	"errors"
	"io"
	"net/http"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/function"
	"github.com/goabonga/infrastructure/internal/state"
)

// FunctionInvokeHandler serves the synchronous function-invoke route,
// alongside - not instead of - the generic CRUD routes already registered
// for the function and function_instance kinds.
type FunctionInvokeHandler struct {
	svc *function.Service
}

// NewFunctionInvokeHandler returns a handler backed by svc.
func NewFunctionInvokeHandler(svc *function.Service) *FunctionInvokeHandler {
	return &FunctionInvokeHandler{svc: svc}
}

// Register mounts the invoke route under base (e.g. "/api/v1").
func (h *FunctionInvokeHandler) Register(mux *http.ServeMux, base string) {
	mux.HandleFunc("POST "+base+"/"+resource.KindFunction+"/{uid}/invoke", h.invoke)
}

func (h *FunctionInvokeHandler) invoke(w http.ResponseWriter, r *http.Request) {
	resp, err := h.svc.Invoke(r.Context(), r.PathValue("uid"), r.Body, r.Header.Get("Content-Type"))
	switch {
	case errors.Is(err, state.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, function.ErrNotReady), errors.Is(err, function.ErrNoWarmInstance):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		defer resp.Body.Close()
		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}
