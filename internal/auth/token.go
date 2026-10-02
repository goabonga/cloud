// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package auth

import (
	"fmt"
	"net/http"
	"strings"
)

// TokenAuthenticator authenticates requests by a static bearer token mapped to
// a subject. It is the bootstrap mechanism ahead of OIDC.
type TokenAuthenticator struct {
	tokens map[string]string // token -> subject
}

// NewTokenAuthenticator copies tokens (token -> subject) into a new authenticator.
func NewTokenAuthenticator(tokens map[string]string) *TokenAuthenticator {
	m := make(map[string]string, len(tokens))
	for token, subject := range tokens {
		m[token] = subject
	}
	return &TokenAuthenticator{tokens: m}
}

// Authenticate resolves the bearer token in the Authorization header.
func (a *TokenAuthenticator) Authenticate(r *http.Request) (*Identity, error) {
	token, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		return nil, ErrUnauthenticated
	}
	subject, ok := a.tokens[token]
	if !ok {
		return nil, ErrUnauthenticated
	}
	return &Identity{Subject: subject}, nil
}

func bearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

// ParseTokens parses a "token1:subject1,token2:subject2" specification.
func ParseTokens(spec string) (map[string]string, error) {
	tokens := make(map[string]string)
	for _, pair := range strings.Split(spec, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		token, subject, ok := strings.Cut(pair, ":")
		if !ok || token == "" || subject == "" {
			return nil, fmt.Errorf("auth: invalid token entry %q (want token:subject)", pair)
		}
		tokens[token] = subject
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("auth: no tokens parsed")
	}
	return tokens, nil
}
