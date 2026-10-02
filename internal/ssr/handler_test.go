// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package ssr

import (
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestApplicationRoutes(t *testing.T) {
	files := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<h1>cloud</h1>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("export const cloud = true;")},
		".env":          &fstest.MapFile{Data: []byte("secret")},
	}
	handler, err := New(files, "0.0.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/", "<h1>cloud</h1>", 200},
		{"GET", "/settings/profile", "<h1>cloud</h1>", 200},
		{"GET", "/assets/app.js", "export const cloud", 200},
		{"HEAD", "/assets/app.js", "", 200},
		{"GET", "/healthz", "cloud-ssr", 200},
		{"GET", "/assets/missing.js", "", 404},
		{"GET", "/assets/", "", 404},
		{"GET", "/.env", "", 404},
		{"GET", "/api/resources", "", 404},
		{"GET", "/idp/login", "", 404},
		{"POST", "/", "", 405},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, nil))
		if recorder.Code != tc.status || !strings.Contains(recorder.Body.String(), tc.body) {
			t.Fatalf("%s %s: %d %q", tc.method, tc.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestMissingFrontendBuildFailsAtStartup(t *testing.T) {
	if _, err := New(fstest.MapFS{}, "0.0.0"); err == nil {
		t.Fatal("missing index was accepted")
	}
}
