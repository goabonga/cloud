// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package secret_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/goabonga/infrastructure/internal/crypto"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/secret"
	"github.com/goabonga/infrastructure/internal/state"
)

func newService(t *testing.T) *secret.Service {
	t.Helper()
	key, _ := crypto.GenerateKey()
	kek, err := crypto.NewKEK(key)
	if err != nil {
		t.Fatalf("kek: %v", err)
	}
	store := state.NewFileStore(t.TempDir())
	reg := registry.New[resource.SecretSpec, resource.SecretStatus](store, resource.KindSecret)
	versions := registry.New[resource.SecretVersionSpec, resource.SecretVersionStatus](store, resource.KindSecretVersion)
	return secret.NewService(reg, versions, kek)
}

func TestSecretPutRedactsAndReveals(t *testing.T) {
	t.Parallel()

	svc := newService(t)
	out, err := svc.Put("sec-1", "db-password", "s3cr3t")
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if out.Spec.Data != "" || out.Status.Ciphertext != nil {
		t.Fatalf("put response leaks secret material: %+v", out)
	}
	if !out.Status.IsReady() || out.Metadata.Generation != 1 {
		t.Fatalf("unexpected meta/status: %+v %+v", out.Metadata, out.Status)
	}

	got, err := svc.Get("sec-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Spec.Data != "" || got.Status.Ciphertext != nil {
		t.Fatal("get response leaks secret material")
	}

	plaintext, err := svc.Reveal("sec-1")
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}
	if plaintext != "s3cr3t" {
		t.Fatalf("reveal = %q, want s3cr3t", plaintext)
	}
}

func TestSecretVersionsIncrementAndReveal(t *testing.T) {
	t.Parallel()

	svc := newService(t)
	v1, err := svc.AddVersion("ver-1", "sec-1", "first")
	if err != nil {
		t.Fatalf("add v1: %v", err)
	}
	if v1.Status.Version != 1 {
		t.Fatalf("version = %d, want 1", v1.Status.Version)
	}
	if v1.Spec.Data != "" || v1.Status.Ciphertext != nil {
		t.Fatalf("version response leaks material: %+v", v1)
	}
	v2, err := svc.AddVersion("ver-2", "sec-1", "second")
	if err != nil {
		t.Fatalf("add v2: %v", err)
	}
	if v2.Status.Version != 2 {
		t.Fatalf("version = %d, want 2", v2.Status.Version)
	}
	// A version of a different secret restarts numbering.
	other, err := svc.AddVersion("ver-3", "sec-2", "x")
	if err != nil {
		t.Fatalf("add other: %v", err)
	}
	if other.Status.Version != 1 {
		t.Fatalf("other version = %d, want 1", other.Status.Version)
	}

	list, err := svc.ListVersions("sec-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].Status.Version != 1 || list[1].Status.Version != 2 {
		t.Fatalf("unexpected version list: %+v", list)
	}

	plain, err := svc.RevealVersion("ver-2")
	if err != nil {
		t.Fatalf("reveal: %v", err)
	}
	if plain != "second" {
		t.Fatalf("revealed %q, want second", plain)
	}

	if err := svc.DeleteVersion("ver-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.GetVersion("ver-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("version should be gone, got %v", err)
	}
}

func TestSecretNotStoredInClear(t *testing.T) {
	t.Parallel()

	key, _ := crypto.GenerateKey()
	kek, _ := crypto.NewKEK(key)
	store := state.NewFileStore(t.TempDir())
	reg := registry.New[resource.SecretSpec, resource.SecretStatus](store, resource.KindSecret)
	versions := registry.New[resource.SecretVersionSpec, resource.SecretVersionStatus](store, resource.KindSecretVersion)
	svc := secret.NewService(reg, versions, kek)

	if _, err := svc.Put("sec-1", "", "p@ssw0rd-plaintext"); err != nil {
		t.Fatalf("put: %v", err)
	}
	// Read the raw stored bytes: the plaintext must not appear.
	raw, err := store.Get("secret/sec-1")
	if err != nil {
		t.Fatalf("raw get: %v", err)
	}
	if string(raw) == "" {
		t.Fatal("nothing stored")
	}
	if bytes.Contains(raw, []byte("p@ssw0rd-plaintext")) {
		t.Fatal("plaintext stored at rest")
	}
}

func TestSecretPutBumpsGeneration(t *testing.T) {
	t.Parallel()

	svc := newService(t)
	if _, err := svc.Put("sec-1", "n", "v1"); err != nil {
		t.Fatalf("put v1: %v", err)
	}
	out, err := svc.Put("sec-1", "n", "v2")
	if err != nil {
		t.Fatalf("put v2: %v", err)
	}
	if out.Metadata.Generation != 2 {
		t.Fatalf("generation = %d, want 2", out.Metadata.Generation)
	}
	if rev, _ := svc.Reveal("sec-1"); rev != "v2" {
		t.Fatalf("reveal = %q, want v2", rev)
	}
}

func TestSecretValidationAndDelete(t *testing.T) {
	t.Parallel()

	svc := newService(t)
	if _, err := svc.Put("", "n", "v"); err == nil {
		t.Fatal("expected error for empty uid")
	}
	if _, err := svc.Put("sec-1", "n", ""); err == nil {
		t.Fatal("expected error for empty data")
	}

	if _, err := svc.Put("sec-1", "n", "v"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := svc.Delete("sec-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get("sec-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
