// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package web serves the single-page dashboard and proxies API calls to the
// control plane, so the browser talks to one origin and the Bearer token flows
// straight through to the API.
package web

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/goabonga/infrastructure/internal/httpsec"
)

// securityTxt is the RFC 9116 advisory served at /.well-known/security.txt.
const securityTxt = "Contact: mailto:goabonga@pm.me\nPreferred-Languages: en, fr\n"

// Server serves the embedded SPA and reverse-proxies /api to the control plane.
type Server struct {
	mux    *http.ServeMux
	static fs.FS
}

// Options configures the web server.
type Options struct {
	// APIBaseURL is the control-plane API to proxy /api requests to.
	APIBaseURL string
	// IDPBaseURL is the identity provider to proxy /idp requests to (its
	// own routes sit at its origin's root, e.g. /login, /userinfo - the
	// /idp prefix is stripped before forwarding).
	IDPBaseURL string
}

// New builds a Server serving the SPA from static and proxying /api to the API
// and /idp to the identity provider.
func New(static fs.FS, opts Options) (*Server, error) {
	s := &Server{mux: http.NewServeMux(), static: static}

	if opts.APIBaseURL != "" {
		target, err := url.Parse(opts.APIBaseURL)
		if err != nil {
			return nil, fmt.Errorf("web: invalid API URL %q: %w", opts.APIBaseURL, err)
		}
		s.mux.Handle("/api/", httputil.NewSingleHostReverseProxy(target))
	}

	if opts.IDPBaseURL != "" {
		target, err := url.Parse(opts.IDPBaseURL)
		if err != nil {
			return nil, fmt.Errorf("web: invalid IDP URL %q: %w", opts.IDPBaseURL, err)
		}
		s.mux.Handle("/idp/", http.StripPrefix("/idp", httputil.NewSingleHostReverseProxy(target)))
	}

	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	s.mux.HandleFunc("GET /.well-known/security.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(securityTxt))
	})
	s.mux.HandleFunc("/", s.spa)

	return s, nil
}

// Handler returns the routed HTTP handler.
func (s *Server) Handler() http.Handler {
	return httpsec.Headers(s.mux)
}

// spa serves a static asset when it exists and falls back to index.html for
// client-side routes.
func (s *Server) spa(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name != "" && s.exists(name) {
		http.FileServerFS(s.static).ServeHTTP(w, r)
		return
	}
	s.serveIndex(w)
}

func (s *Server) exists(name string) bool {
	f, err := s.static.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	return err == nil && !info.IsDir()
}

func (s *Server) serveIndex(w http.ResponseWriter) {
	f, err := s.static.Open("index.html")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "frontend not built", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.Copy(w, f)
}
