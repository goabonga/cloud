// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package auth

import "net/http"

// chain tries a list of Authenticators in order, so e.g. a short-lived JWT
// and a long-lived introspected access token can both authenticate against
// the same server. A token that isn't JWT-shaped makes JWTAuthenticator fail
// immediately with no network call, so ordering it before an
// IntrospectionAuthenticator costs nothing on the common case.
type chain struct {
	authns []Authenticator
}

// Chain combines authns into one Authenticator that returns the first
// success, or the last error if every one of them fails.
func Chain(authns ...Authenticator) Authenticator {
	return &chain{authns: authns}
}

func (c *chain) Authenticate(r *http.Request) (*Identity, error) {
	lastErr := ErrUnauthenticated
	for _, a := range c.authns {
		id, err := a.Authenticate(r)
		if err == nil {
			return id, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
