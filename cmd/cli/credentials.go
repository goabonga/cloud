// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// credentials is a token obtained via `infra login`, persisted between
// invocations since this CLI otherwise only ever reads GOA_API_TOKEN from the
// environment for a single run.
type credentials struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func credentialsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "infra", "credentials.json"), nil
}

// saveCredentials writes c to the user's config directory, readable only by
// the owner since it carries a bearer token.
func saveCredentials(c credentials) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// loadCredentials returns the persisted credentials, or false if there are
// none, they cannot be read, or they have expired.
func loadCredentials() (credentials, bool) {
	path, err := credentialsPath()
	if err != nil {
		return credentials{}, false
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path is derived from os.UserConfigDir(), not user input
	if err != nil {
		return credentials{}, false
	}
	var c credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return credentials{}, false
	}
	if time.Now().After(c.ExpiresAt) {
		return credentials{}, false
	}
	return c, true
}
