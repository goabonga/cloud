// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Command infra-www serves the dashboard single-page app and reverse-proxies
// /api to the control plane and /idp to the identity provider.
package main

import (
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/goabonga/infrastructure/internal/meta"
	"github.com/goabonga/infrastructure/internal/web"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("infra-www: %v", err)
	}
}

func run() error {
	addr := flag.String("addr", envOr("GOA_WWW_ADDR", ":8088"), "listen address")
	apiURL := flag.String("api", envOr("GOA_API_URL", "http://localhost:8080"), "control-plane API URL to proxy")
	idpURL := flag.String("idp", envOr("GOA_IDP_URL", "http://localhost:8081"), "identity provider URL to proxy")
	flag.Parse()

	static, err := fs.Sub(distFS, "dist")
	if err != nil {
		return err
	}
	srv, err := web.New(static, web.Options{APIBaseURL: *apiURL, IDPBaseURL: *idpURL})
	if err != nil {
		return err
	}

	log.Printf("%s listening on %s (api %s, idp %s)", meta.Line("infra-www", Version), *addr, *apiURL, *idpURL)
	httpSrv := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	return httpSrv.ListenAndServe()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
