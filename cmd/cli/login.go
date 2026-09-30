// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

func idpURL() string {
	if v := os.Getenv("GOA_IDP_URL"); v != "" {
		return v
	}
	return "http://localhost:8081"
}

// runLogin obtains a token via the OAuth2 device authorization grant and
// persists it, so a following `infra vpc ...` picks it up without
// GOA_API_TOKEN having to be set.
func runLogin(ctx context.Context, stdout io.Writer) error {
	token, expiresIn, err := deviceLogin(ctx, idpURL(), func(userCode, verificationURI string) {
		_, _ = fmt.Fprintf(stdout, "First copy your one-time code: %s\n", userCode)
		_, _ = fmt.Fprintf(stdout, "Then open: %s\n", verificationURI)
		_, _ = fmt.Fprintln(stdout, "Waiting for approval...")
	})
	if err != nil {
		return err
	}
	if err := saveCredentials(credentials{Token: token, ExpiresAt: time.Now().Add(time.Duration(expiresIn) * time.Second)}); err != nil {
		return fmt.Errorf("save credentials: %w", err)
	}
	_, _ = fmt.Fprintln(stdout, "Logged in.")
	return nil
}

type deviceAuthorizationResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// deviceLogin runs RFC 8628 against baseURL: it starts the authorization,
// calls onCode once the code is available (production prints it; tests
// approve it immediately), then polls until a human resolves it or the code
// expires.
func deviceLogin(ctx context.Context, baseURL string, onCode func(userCode, verificationURI string)) (token string, expiresIn int, err error) {
	resp, err := http.PostForm(baseURL+"/device_authorization", url.Values{})
	if err != nil {
		return "", 0, fmt.Errorf("device_authorization: %w", err)
	}
	var auth deviceAuthorizationResponse
	decodeErr := json.NewDecoder(resp.Body).Decode(&auth)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("device_authorization: status %d", resp.StatusCode)
	}
	if decodeErr != nil {
		return "", 0, fmt.Errorf("decode device_authorization: %w", decodeErr)
	}

	verificationURI := auth.VerificationURIComplete
	if verificationURI == "" {
		verificationURI = auth.VerificationURI
	}
	onCode(auth.UserCode, verificationURI)

	interval := time.Duration(auth.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(auth.ExpiresIn) * time.Second)

	// Poll right away - the human may have approved before onCode even
	// returned - then wait interval between subsequent attempts.
	for {
		tok, exp, pending, err := pollDeviceToken(baseURL, auth.DeviceCode)
		if err != nil {
			return "", 0, err
		}
		if !pending {
			return tok, exp, nil
		}
		if time.Now().After(deadline) {
			return "", 0, fmt.Errorf("device login: code expired")
		}
		select {
		case <-ctx.Done():
			return "", 0, ctx.Err()
		case <-time.After(interval):
		}
	}
}

func pollDeviceToken(baseURL, deviceCode string) (token string, expiresIn int, pending bool, err error) {
	resp, err := http.PostForm(baseURL+"/token", url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceCode},
	})
	if err != nil {
		return "", 0, false, fmt.Errorf("poll token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", 0, false, fmt.Errorf("decode token response: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusOK:
		return out.AccessToken, out.ExpiresIn, false, nil
	case out.Error == "authorization_pending":
		return "", 0, true, nil
	case out.Error == "access_denied":
		return "", 0, false, fmt.Errorf("device login: access denied")
	case out.Error == "expired_token":
		return "", 0, false, fmt.Errorf("device login: code expired")
	default:
		return "", 0, false, fmt.Errorf("poll token: %s", out.Error)
	}
}
