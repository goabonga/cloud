// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package httpsec provides the response-header middleware shared by the
// control-plane HTTP servers.
//
// The headers here are the ones a browser acts on, and their absence is what a
// DAST pass reports against both servers. They live in one package rather than
// being set per handler so the API and the dashboard cannot drift apart: a
// header added for one and forgotten for the other is the failure mode this
// exists to remove.
package httpsec

import "net/http"

// Headers wraps next and sets the baseline security headers on every response.
//
// Two variants are deliberately not offered. The API and the dashboard get the
// same set even though a JSON API is not framable and does not execute inline
// script, because a header that costs nothing on one surface and matters on the
// other is not worth a conditional - and the API's JSON is rendered by the
// dashboard, so it inherits the same threat model.
func Headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()

		// Refuse MIME sniffing. Without this a browser may execute a JSON
		// response it decides looks like script, which is the whole point of
		// the header on an API that reflects user-supplied resource fields.
		h.Set("X-Content-Type-Options", "nosniff")

		// Deny framing. The dashboard shows cluster state and issues
		// mutations, so clickjacking it is worth the two headers - the older
		// X-Frame-Options for anything that predates frame-ancestors.
		h.Set("X-Frame-Options", "DENY")

		// The SPA is a Vite bundle: hashed script and style files, no inline
		// script, no external origins. `self` covers all of it, and anything
		// that later needs an exception should have to state it here.
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self'; "+
				"style-src 'self'; "+
				"img-src 'self' data:; "+
				"connect-src 'self'; "+
				"font-src 'self'; "+
				"object-src 'none'; "+
				"base-uri 'none'; "+
				"form-action 'self'; "+
				"frame-ancestors 'none'")

		// Do not leak resource UIDs through the Referer header on outbound
		// navigation. Paths here carry identifiers.
		h.Set("Referrer-Policy", "no-referrer")

		// Nothing in the control plane uses a camera, microphone or location.
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")

		// HSTS is set only on a request that already arrived over TLS. Sending
		// it over plaintext HTTP is meaningless, and in a development setup
		// served on http://localhost it would pin the browser to a scheme
		// nothing is listening on.
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}
