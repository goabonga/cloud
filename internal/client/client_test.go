// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package client_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/goabonga/infrastructure/internal/auth"
	"github.com/goabonga/infrastructure/internal/client"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/httpsrv"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func newClient(t *testing.T) *client.Client[resource.VPCSpec, resource.VPCStatus] {
	t.Helper()
	srv := httptest.NewServer(httpsrv.New(state.NewFileStore(t.TempDir())).Handler())
	t.Cleanup(srv.Close)
	return client.New[resource.VPCSpec, resource.VPCStatus](srv.URL, resource.KindVPC)
}

func TestClientRoundtrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := newClient(t)

	// Missing resource.
	if _, err := c.Get(ctx, "vpc-1"); !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// Create.
	in := &resource.VPC{
		Metadata: resource.ObjectMeta{UID: "vpc-1", Name: "prod"},
		Spec:     resource.VPCSpec{CIDR: "10.0.0.0/16"},
	}
	out, err := c.Put(ctx, in)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if out.Metadata.Generation != 1 {
		t.Fatalf("generation = %d, want 1", out.Metadata.Generation)
	}

	// Get.
	got, err := c.Get(ctx, "vpc-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Spec.CIDR != "10.0.0.0/16" {
		t.Fatalf("cidr = %q", got.Spec.CIDR)
	}

	// List.
	items, err := c.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("list len = %d, want 1", len(items))
	}

	// Delete.
	if err := c.Delete(ctx, "vpc-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := c.Get(ctx, "vpc-1"); !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestClientSendsBearerToken(t *testing.T) {
	t.Parallel()

	tokenAuth := auth.NewTokenAuthenticator(map[string]string{"tok": "alice"})
	srv := httptest.NewServer(httpsrv.New(state.NewFileStore(t.TempDir()), httpsrv.WithAuth(tokenAuth)).Handler())
	t.Cleanup(srv.Close)
	ctx := context.Background()

	// Without a token the API rejects the request.
	noToken := client.New[resource.VPCSpec, resource.VPCStatus](srv.URL, resource.KindVPC)
	if _, err := noToken.List(ctx); err == nil {
		t.Fatal("expected an error without a token")
	}

	// With the token the request is accepted.
	withToken := client.New[resource.VPCSpec, resource.VPCStatus](srv.URL, resource.KindVPC, client.WithToken("tok"))
	if _, err := withToken.List(ctx); err != nil {
		t.Fatalf("list with token: %v", err)
	}
}

func TestClientRejectsInvalidSpec(t *testing.T) {
	t.Parallel()

	c := newClient(t)
	in := &resource.VPC{
		Metadata: resource.ObjectMeta{UID: "vpc-bad"},
		Spec:     resource.VPCSpec{CIDR: "not-a-cidr"},
	}
	if _, err := c.Put(context.Background(), in); err == nil {
		t.Fatal("expected error for invalid spec")
	}
}

func TestClientListFollowsServerPagination(t *testing.T) {
	store := state.NewFileStore(t.TempDir())
	reg := registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC)
	for i := range 205 {
		if err := reg.Put(&resource.VPC{Metadata: resource.ObjectMeta{UID: fmt.Sprintf("vpc-%03d", i)}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(httpsrv.New(store).Handler())
	defer srv.Close()
	c := client.New[resource.VPCSpec, resource.VPCStatus](srv.URL, resource.KindVPC)
	items, err := c.List(t.Context())
	if err != nil || len(items) != 205 {
		t.Fatalf("list: %d items, %v", len(items), err)
	}
}
