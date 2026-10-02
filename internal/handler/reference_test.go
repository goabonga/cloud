// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package handler_test

import (
	"net/http"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/handler"
	"github.com/goabonga/infrastructure/internal/iam"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func TestReferencesCannotCrossOwnership(t *testing.T) {
	store := state.NewFileStore(t.TempDir())
	vpcs := registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC)
	if err := vpcs.Put(&resource.VPC{Metadata: resource.ObjectMeta{UID: "private", OwnerUID: "alice"}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}); err != nil {
		t.Fatal(err)
	}
	subnets := registry.New[resource.SubnetSpec, resource.SubnetStatus](store, resource.KindSubnet)
	mux := http.NewServeMux()
	az := &iam.Authorizer{}
	handler.New(subnets, resource.KindSubnet, handler.WithAuthorization[resource.SubnetSpec, resource.SubnetStatus](az)).Register(mux, "/api/v1")
	input := resource.Subnet{Spec: resource.SubnetSpec{VPCID: "private", CIDR: "10.0.0.0/24"}}
	if got := doAs(t, mux, "bob", nil, "PUT", "/api/v1/subnet/s", input); got.Code != http.StatusForbidden {
		t.Fatalf("foreign VPC: %d %s", got.Code, got.Body)
	}
	if got := doAs(t, mux, "alice", nil, "PUT", "/api/v1/subnet/s", input); got.Code != http.StatusCreated {
		t.Fatalf("own VPC: %d %s", got.Code, got.Body)
	}
}

func TestReferencesCannotTraverseKinds(t *testing.T) {
	store := state.NewFileStore(t.TempDir())
	reg := registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC)
	if err := store.Put("secret/private", []byte(`{"metadata":{"ownerUid":"alice"}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.LookupMetadata(resource.KindVPC, "../secret/private"); err == nil {
		t.Fatal("reference escaped its kind")
	}
}
