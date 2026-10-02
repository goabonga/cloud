// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/httpsrv"
	"github.com/goabonga/infrastructure/internal/state"
)

func startServer(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(httpsrv.New(state.NewFileStore(t.TempDir())).Handler())
	t.Cleanup(srv.Close)
	t.Setenv("GOA_API_URL", srv.URL)
}

func TestRunUsageErrors(t *testing.T) {
	if err := run(context.Background(), nil, &bytes.Buffer{}); err == nil {
		t.Fatal("expected usage error with no args")
	}
	if err := run(context.Background(), []string{"bogus"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error for unknown command")
	}
	if err := run(context.Background(), []string{"vpc"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected usage error for bare vpc")
	}
	if err := run(context.Background(), []string{"vpc", "get"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected usage error for vpc get without uid")
	}
}

func TestRunVPCLifecycle(t *testing.T) {
	startServer(t)
	ctx := context.Background()

	var out bytes.Buffer
	if err := run(ctx, []string{"vpc", "create", "vpc-1", "--cidr", "10.0.0.0/16", "--name", "prod"}, &out); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(out.String(), "vpc-1") {
		t.Fatalf("create output missing uid: %s", out.String())
	}

	out.Reset()
	if err := run(ctx, []string{"vpc", "list"}, &out); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out.String(), "10.0.0.0/16") {
		t.Fatalf("list output missing cidr: %s", out.String())
	}

	out.Reset()
	if err := run(ctx, []string{"vpc", "get", "vpc-1"}, &out); err != nil {
		t.Fatalf("get: %v", err)
	}

	if err := run(ctx, []string{"vpc", "delete", "vpc-1"}, &bytes.Buffer{}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := run(ctx, []string{"vpc", "get", "vpc-1"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected error getting deleted vpc")
	}
}

func TestRunVPCCreateValidates(t *testing.T) {
	startServer(t)
	if err := run(context.Background(), []string{"vpc", "create", "vpc-bad", "--cidr", "nope"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected create to fail on invalid cidr")
	}
}
