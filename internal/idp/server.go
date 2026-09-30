// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package idp

import (
	"crypto/ecdsa"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
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
	"/device_authorization":             true,
	"/jwks.json":                        true,
	"/.well-known/openid-configuration": true,
	"/healthz":                          true,
}

// Server is the identity provider HTTP surface: an OAuth2 client-credentials
// token endpoint, a password login endpoint for human users, a device
// authorization grant, user management, and JWKS/discovery documents.
type Server struct {
	mux        *http.ServeMux
	issuer     *Issuer
	clients    map[string]string // client_id -> client_secret
	users      *identity.Service
	devices    *deviceStore
	authn      auth.Authenticator
	pub        *ecdsa.PublicKey
	issuerURL  string
	consoleURL string
}

// NewServer builds an IdP server. clients maps client_id to client_secret for
// the client-credentials grant; users backs password login and user
// management; pub is the public half of the issuer key; issuerURL is the
// externally reachable base URL used in discovery and the issuer claim;
// consoleURL is where a human approves a device authorization (idp has no
// concept of "www" beyond this - it is just the console's own address).
func NewServer(issuer *Issuer, clients map[string]string, users *identity.Service, pub *ecdsa.PublicKey, issuerURL, consoleURL string) *Server {
	s := &Server{
		mux:        http.NewServeMux(),
		issuer:     issuer,
		clients:    clients,
		users:      users,
		devices:    newDeviceStore(deviceCodeTTL),
		authn:      auth.NewJWTAuthenticator(pub, issuerURL),
		pub:        pub,
		issuerURL:  issuerURL,
		consoleURL: consoleURL,
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("POST /token", s.token)
	s.mux.HandleFunc("POST /login", s.login)
	s.mux.HandleFunc("POST /device_authorization", s.deviceAuthorization)
	s.mux.HandleFunc("POST /device/verify", s.deviceVerify)
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

// token dispatches on grant_type: client_credentials (the default, for
// backward compatibility with clients that omit it) or the device code grant.
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "", "client_credentials":
		s.clientCredentialsGrant(w, r)
	case deviceGrantType:
		s.deviceGrant(w, r)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
	}
}

// clientCredentialsGrant implements the OAuth2 client-credentials grant.
func (s *Server) clientCredentialsGrant(w http.ResponseWriter, r *http.Request) {
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

// deviceGrant implements the polling half of RFC 8628: the client trades a
// device_code for a token once a human has approved it at /device/verify.
func (s *Server) deviceGrant(w http.ResponseWriter, r *http.Request) {
	deviceCode := r.PostForm.Get("device_code")
	if deviceCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	da, ok := s.devices.byDeviceCode(deviceCode)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expired_token"})
		return
	}
	switch da.status {
	case deviceStatusPending:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "authorization_pending"})
	case deviceStatusDenied:
		s.devices.delete(deviceCode)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "access_denied"})
	case deviceStatusApproved:
		token, err := s.issuer.Issue(da.subject, da.roles)
		s.devices.delete(deviceCode)
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

type deviceAuthorizationResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// deviceAuthorization starts a device authorization: it returns a code for
// the polling client and a code plus a link for the human approving it.
func (s *Server) deviceAuthorization(w http.ResponseWriter, _ *http.Request) {
	da, err := s.devices.create()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}
	verificationURI := s.consoleURL + "/device"
	writeJSON(w, http.StatusOK, deviceAuthorizationResponse{
		DeviceCode:              da.deviceCode,
		UserCode:                da.userCode,
		VerificationURI:         verificationURI,
		VerificationURIComplete: verificationURI + "?user_code=" + url.QueryEscape(da.userCode),
		ExpiresIn:               int(deviceCodeTTL.Seconds()),
		Interval:                devicePollInterval,
	})
}

// deviceVerify approves or denies a pending device authorization on behalf of
// the authenticated caller, who becomes the subject of the resulting token.
func (s *Server) deviceVerify(w http.ResponseWriter, r *http.Request) {
	id, ok := auth.IdentityFrom(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	var in struct {
		UserCode string `json:"user_code"`
		Approve  bool   `json:"approve"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	var resolved bool
	if in.Approve {
		resolved = s.devices.approve(in.UserCode, id.Subject, id.Roles)
	} else {
		resolved = s.devices.deny(in.UserCode)
	}
	if !resolved {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		"device_authorization_endpoint":         s.issuerURL + "/device_authorization",
		"jwks_uri":                              s.issuerURL + "/jwks.json",
		"grant_types_supported":                 []string{"client_credentials", "password", deviceGrantType},
		"id_token_signing_alg_values_supported": []string{"ES256"},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
