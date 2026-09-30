// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package idp

import (
	"crypto/ecdsa"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"time"
)

// Server is the identity provider HTTP surface: an OAuth2 client-credentials
// token endpoint plus JWKS and discovery documents.
type Server struct {
	mux       *http.ServeMux
	issuer    *Issuer
	clients   map[string]string // client_id -> client_secret
	pub       *ecdsa.PublicKey
	issuerURL string
}

// NewServer builds an IdP server. clients maps client_id to client_secret; pub
// is the public half of the issuer key; issuerURL is the externally reachable
// base URL used in discovery and the issuer claim.
func NewServer(issuer *Issuer, clients map[string]string, pub *ecdsa.PublicKey, issuerURL string) *Server {
	s := &Server{
		mux:       http.NewServeMux(),
		issuer:    issuer,
		clients:   clients,
		pub:       pub,
		issuerURL: issuerURL,
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("POST /token", s.token)
	s.mux.HandleFunc("GET /jwks.json", s.jwks)
	s.mux.HandleFunc("GET /.well-known/openid-configuration", s.discovery)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

// Handler exposes the routed handler for serving and tests.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// ListenAndServe runs the IdP on addr.
func (s *Server) ListenAndServe(addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.mux, ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// token implements the OAuth2 client-credentials grant.
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	clientID := r.PostForm.Get("client_id")
	clientSecret := r.PostForm.Get("client_secret")
	if !s.validClient(clientID, clientSecret) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return
	}
	token, err := s.issuer.Issue(clientID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   int(s.issuer.TTL().Seconds()),
	})
}

func (s *Server) validClient(id, secret string) bool {
	want, ok := s.clients[id]
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(secret), []byte(want)) == 1
}

func (s *Server) jwks(w http.ResponseWriter, _ *http.Request) {
	ks, err := PublicJWKS(s.pub, "infra-idp")
	if err != nil {
		http.Error(w, "jwks unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, ks)
}

func (s *Server) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                s.issuerURL,
		"token_endpoint":                        s.issuerURL + "/token",
		"jwks_uri":                              s.issuerURL + "/jwks.json",
		"grant_types_supported":                 []string{"client_credentials"},
		"id_token_signing_alg_values_supported": []string{"ES256"},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
