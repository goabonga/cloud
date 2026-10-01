// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

func TestRunBuildsServeOpts(t *testing.T) {
	var called bool
	var gotOpts providerserver.ServeOpts
	fake := func(_ context.Context, providerFunc func() provider.Provider, opts providerserver.ServeOpts) error {
		called = true
		gotOpts = opts
		if providerFunc == nil || providerFunc() == nil {
			t.Fatal("expected a non-nil provider factory")
		}
		return nil
	}

	if err := run(context.Background(), nil, &bytes.Buffer{}, fake); err != nil {
		t.Fatalf("run() = %v, want nil", err)
	}
	if !called {
		t.Fatal("expected serve to be called")
	}
	if gotOpts.Address != "registry.terraform.io/goabonga/infra" {
		t.Fatalf("Address = %q", gotOpts.Address)
	}
	if gotOpts.Debug {
		t.Fatal("expected Debug=false by default")
	}
}

func TestRunDebugFlag(t *testing.T) {
	var gotOpts providerserver.ServeOpts
	fake := func(_ context.Context, _ func() provider.Provider, opts providerserver.ServeOpts) error {
		gotOpts = opts
		return nil
	}

	if err := run(context.Background(), []string{"-debug"}, &bytes.Buffer{}, fake); err != nil {
		t.Fatalf("run() = %v, want nil", err)
	}
	if !gotOpts.Debug {
		t.Fatal("expected Debug=true")
	}
}

func TestRunBadFlag(t *testing.T) {
	called := false
	fake := func(context.Context, func() provider.Provider, providerserver.ServeOpts) error {
		called = true
		return nil
	}

	if err := run(context.Background(), []string{"-bogus"}, &bytes.Buffer{}, fake); err == nil {
		t.Fatal("expected error for unknown flag")
	}
	if called {
		t.Fatal("serve should not be called when flag parsing fails")
	}
}

func TestRunServeError(t *testing.T) {
	sentinel := errors.New("boom")
	fake := func(context.Context, func() provider.Provider, providerserver.ServeOpts) error {
		return sentinel
	}

	if err := run(context.Background(), nil, &bytes.Buffer{}, fake); !errors.Is(err, sentinel) {
		t.Fatalf("run() = %v, want %v", err, sentinel)
	}
}
