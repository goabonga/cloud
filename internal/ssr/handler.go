// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package ssr serves the built browser application from a supplied filesystem.
package ssr

import (
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/goabonga/cloud/internal/transport"
)

// New validates the build entry point and serves assets with SPA navigation.
func New(assets fs.FS, version string) (http.Handler, error) {
	index, err := fs.Stat(assets, "index.html")
	if err != nil {
		return nil, fmt.Errorf("frontend build: %w", err)
	}
	if !index.Mode().IsRegular() {
		return nil, fmt.Errorf("frontend index must be a regular file")
	}
	static := http.FileServerFS(assets)
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", transport.Health("cloud-ssr", version))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if strings.HasPrefix(name, ".") || strings.Contains(name, "/.") {
			http.NotFound(w, r)
			return
		}
		info, err := fs.Stat(assets, name)
		if err == nil && info.Mode().IsRegular() {
			static.ServeHTTP(w, r)
			return
		}
		if name == "api" || strings.HasPrefix(name, "api/") || name == "idp" || strings.HasPrefix(name, "idp/") || path.Ext(name) != "" || (err == nil && info.IsDir() && name != ".") {
			http.NotFound(w, r)
			return
		}
		// Serve the SPA entry point without changing the request seen upstream.
		clone := r.Clone(r.Context())
		clone.URL.Path = "/"
		static.ServeHTTP(w, clone)
	})
	return mux, nil
}
