// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package idp

import (
	"crypto/ecdsa"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/goabonga/infrastructure/internal/auth"
	"github.com/goabonga/infrastructure/internal/handler"
	"github.com/goabonga/infrastructure/internal/identity"
)

// publicPaths never require a bearer token: the client-credentials and
// password grants issue the tokens everything else checks, so they cannot
// themselves require one, and jwks/discovery/health must be reachable by
// anyone resolving or monitoring this server.
var publicPaths = map[string]bool{
	"/token":                            true,
	"/login":                            true,
	"/jwks.json":                        true,
	"/.well-known/openid-configuration": true,
	"/healthz":                          true,
}

// Server is the identity provider HTTP surface: an OAuth2 client-credentials
// token endpoint, a password login endpoint for human users, user management,
// and JWKS/discovery documents.
type Server struct {
	mux       *http.ServeMux
	issuer    *Issuer
	clients   map[string]string // client_id -> client_secret
	users     *identity.Service
	authn     auth.Authenticator
	pub       *ecdsa.PublicKey
	issuerURL string
}

// NewServer builds an IdP server. clients maps client_id to client_secret for
// the client-credentials grant; users backs password login and user
// management; pub is the public half of the issuer key; issuerURL is the
// externally reachable base URL used in discovery and the issuer claim.
func NewServer(issuer *Issuer, clients map[string]string, users *identity.Service, pub *ecdsa.PublicKey, issuerURL string) *Server {
	s := &Server{
		mux:       http.NewServeMux(),
		issuer:    issuer,
		clients:   clients,
		users:     users,
		authn:     auth.NewJWTAuthenticator(pub, issuerURL),
		pub:       pub,
		issuerURL: issuerURL,
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("POST /token", s.token)
	s.mux.HandleFunc("POST /login", s.login)
	s.mux.HandleFunc("GET /userinfo", s.userinfo)
	s.mux.HandleFunc("GET /jwks.json", s.jwks)
	s.mux.HandleFunc("GET /.well-known/openid-configuration", s.discovery)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	handler.NewUserHandler(s.users).Register(s.mux, "")
}

// Handler returns the routed HTTP handler. Every route requires a valid
// bearer token except the grants that issue one and the discovery surface.
func (s *Server) Handler() http.Handler {
	guarded := auth.Middleware(s.authn, s.mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicPaths[r.URL.Path] {
			s.mux.ServeHTTP(w, r)
			return
		}
		guarded.ServeHTTP(w, r)
	})
}

// ListenAndServe runs the IdP on addr.
func (s *Server) ListenAndServe(addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
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
	token, err := s.issuer.Issue(clientID, nil)
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

// login authenticates a human user by username and password, issuing a token
// carrying their roles.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	usr, err := s.users.Authenticate(in.Username, in.Password)
	if errors.Is(err, identity.ErrInvalidCredentials) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_grant"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}
	token, err := s.issuer.Issue(usr.Metadata.UID, usr.Spec.Roles)
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

// userinfo returns the caller's own subject and roles, decoded from their
// bearer token by the auth middleware.
func (s *Server) userinfo(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFrom(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"subject": id.Subject,
		"roles":   id.Roles,
	})
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
		"grant_types_supported":                 []string{"client_credentials", "password"},
		"id_token_signing_alg_values_supported": []string{"ES256"},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
