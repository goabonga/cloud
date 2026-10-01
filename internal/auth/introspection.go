// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// IntrospectionAuthenticator validates opaque access tokens by asking an
// identity provider's RFC 7662 introspection endpoint, authenticating itself
// with client-credentials Basic auth.
type IntrospectionAuthenticator struct {
	introspectURL          string
	clientID, clientSecret string
	hc                     *http.Client
}

// NewIntrospectionAuthenticator returns an authenticator that calls
// introspectURL (e.g. "http://localhost:8081/introspect"), authenticating
// with clientID/clientSecret.
func NewIntrospectionAuthenticator(introspectURL, clientID, clientSecret string) *IntrospectionAuthenticator {
	return &IntrospectionAuthenticator{introspectURL: introspectURL, clientID: clientID, clientSecret: clientSecret, hc: http.DefaultClient}
}

// Authenticate resolves the bearer token via introspection.
func (a *IntrospectionAuthenticator) Authenticate(r *http.Request) (*Identity, error) {
	token, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		return nil, ErrUnauthenticated
	}
	id, err := a.introspect(r.Context(), token)
	if err != nil {
		return nil, ErrUnauthenticated
	}
	return id, nil
}

func (a *IntrospectionAuthenticator) introspect(ctx context.Context, token string) (*Identity, error) {
	// #nosec G704 -- introspectURL is operator-configured at process startup
	// (NewIntrospectionAuthenticator's caller, e.g. GOA_API_IDP_INTROSPECT_URL),
	// never derived from the request being authenticated; the token value
	// that *is* request-derived only ever goes into the form body below.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.introspectURL,
		strings.NewReader(url.Values{"token": {token}}.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(a.clientID, a.clientSecret)

	resp, err := a.hc.Do(req) // #nosec G704 -- see the nosec above: req.URL is the operator-configured introspectURL
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, ErrUnauthenticated
	}

	var out struct {
		Active bool     `json:"active"`
		Sub    string   `json:"sub"`
		Roles  []string `json:"roles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if !out.Active || out.Sub == "" {
		return nil, ErrUnauthenticated
	}
	return &Identity{Subject: out.Sub, Roles: out.Roles}, nil
}
