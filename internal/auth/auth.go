// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package auth authenticates API requests. It defines the Authenticator
// contract, a bearer-token implementation, and middleware that attaches the
// resolved identity to the request context.
package auth

import (
	"context"
	"errors"
	"net/http"
)

// ErrUnauthenticated is returned when a request carries no valid credentials.
var ErrUnauthenticated = errors.New("auth: unauthenticated")

// Identity is the authenticated principal behind a request.
type Identity struct {
	Subject string
	Roles   []string
}

// HasRole reports whether the identity carries the named role.
func (id *Identity) HasRole(role string) bool {
	for _, r := range id.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// Authenticator validates a request and resolves its identity.
type Authenticator interface {
	Authenticate(r *http.Request) (*Identity, error)
}

type ctxKey struct{}

// WithIdentity returns a context carrying id.
func WithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// IdentityFrom returns the identity stored in ctx, if any.
func IdentityFrom(ctx context.Context) (*Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(*Identity)
	return id, ok
}
