// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/identity"
	"github.com/goabonga/infrastructure/internal/idp"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// startIDP returns a running infra-idp test server and its user service, so a
// test can seed a user and approve a device code as them.
func startIDP(t *testing.T) (*httptest.Server, *identity.Service) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	users := identity.NewService(registry.New[resource.UserSpec, resource.UserStatus](state.NewFileStore(t.TempDir()), resource.KindUser))
	srv := idp.NewServer(idp.NewIssuer(key, "http://idp", time.Hour), nil, users, &key.PublicKey, "http://idp", "http://console.example")
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, users
}

func loginForTest(t *testing.T, ts *httptest.Server, username, password string) string {
	t.Helper()
	resp, err := http.Post(ts.URL+"/login", "application/json",
		strings.NewReader(`{"username":"`+username+`","password":"`+password+`"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return out.AccessToken
}

func TestDeviceLoginHappyPath(t *testing.T) {
	ts, users := startIDP(t)
	if _, err := users.Put("user-1", resource.UserSpec{Username: "alice", Password: "s3cr3t", Roles: []string{"admin"}}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	aliceToken := loginForTest(t, ts, "alice", "s3cr3t")

	var sawCode string
	approve := func(userCode, _ string) {
		sawCode = userCode
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/device/verify", strings.NewReader(`{"user_code":"`+userCode+`","approve":true}`))
		req.Header.Set("Authorization", "Bearer "+aliceToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("approve: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("approve status = %d", resp.StatusCode)
		}
	}

	token, expiresIn, err := deviceLogin(context.Background(), ts.URL, approve)
	if err != nil {
		t.Fatalf("deviceLogin: %v", err)
	}
	if token == "" || expiresIn <= 0 {
		t.Fatalf("unexpected result: token=%q expiresIn=%d", token, expiresIn)
	}
	if sawCode == "" {
		t.Fatal("onCode was never called")
	}
}

func TestDeviceLoginDenied(t *testing.T) {
	ts, users := startIDP(t)
	if _, err := users.Put("user-1", resource.UserSpec{Username: "alice", Password: "s3cr3t"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	aliceToken := loginForTest(t, ts, "alice", "s3cr3t")

	deny := func(userCode, _ string) {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/device/verify", strings.NewReader(`{"user_code":"`+userCode+`","approve":false}`))
		req.Header.Set("Authorization", "Bearer "+aliceToken)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("deny: %v", err)
		}
		_ = resp.Body.Close()
	}

	if _, _, err := deviceLogin(context.Background(), ts.URL, deny); err == nil {
		t.Fatal("expected an error after denial")
	}
}
