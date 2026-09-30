// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package identity_test

import (
	"errors"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/identity"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func newService(t *testing.T) *identity.Service {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	reg := registry.New[resource.UserSpec, resource.UserStatus](store, resource.KindUser)
	return identity.NewService(reg)
}

func TestPutRedactsAndHashes(t *testing.T) {
	t.Parallel()

	svc := newService(t)
	out, err := svc.Put("user-1", resource.UserSpec{Username: "alice", Password: "s3cr3t", Roles: []string{"admin"}})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if out.Spec.Password != "" || out.Status.PasswordHash != nil {
		t.Fatalf("put response leaks password material: %+v", out)
	}
	if !out.Status.IsReady() || out.Metadata.Generation != 1 {
		t.Fatalf("unexpected meta/status: %+v %+v", out.Metadata, out.Status)
	}

	got, err := svc.Get("user-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Spec.Password != "" || got.Status.PasswordHash != nil {
		t.Fatal("get response leaks password material")
	}
	if len(got.Spec.Roles) != 1 || got.Spec.Roles[0] != "admin" {
		t.Fatalf("roles = %v, want [admin]", got.Spec.Roles)
	}
}

func TestPutRequiresPasswordOnCreate(t *testing.T) {
	t.Parallel()

	svc := newService(t)
	if _, err := svc.Put("user-1", resource.UserSpec{Username: "alice"}); err == nil {
		t.Fatal("want error creating a user without a password")
	}
}

func TestPutWithoutPasswordKeepsExistingHash(t *testing.T) {
	t.Parallel()

	svc := newService(t)
	if _, err := svc.Put("user-1", resource.UserSpec{Username: "alice", Password: "s3cr3t"}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := svc.Put("user-1", resource.UserSpec{Username: "alice", Roles: []string{"admin"}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := svc.Authenticate("alice", "s3cr3t"); err != nil {
		t.Fatalf("authenticate after password-less update: %v", err)
	}
}

func TestAuthenticate(t *testing.T) {
	t.Parallel()

	svc := newService(t)
	if _, err := svc.Put("user-1", resource.UserSpec{Username: "alice", Password: "s3cr3t", Roles: []string{"admin"}}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := svc.Put("user-2", resource.UserSpec{Username: "bob", Password: "s3cr3t", Disabled: true}); err != nil {
		t.Fatalf("put: %v", err)
	}

	t.Run("ok", func(t *testing.T) {
		t.Parallel()
		usr, err := svc.Authenticate("alice", "s3cr3t")
		if err != nil {
			t.Fatalf("authenticate: %v", err)
		}
		if usr.Metadata.UID != "user-1" {
			t.Fatalf("uid = %q, want user-1", usr.Metadata.UID)
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		t.Parallel()
		if _, err := svc.Authenticate("alice", "nope"); !errors.Is(err, identity.ErrInvalidCredentials) {
			t.Fatalf("err = %v, want ErrInvalidCredentials", err)
		}
	})

	t.Run("unknown user", func(t *testing.T) {
		t.Parallel()
		if _, err := svc.Authenticate("nobody", "s3cr3t"); !errors.Is(err, identity.ErrInvalidCredentials) {
			t.Fatalf("err = %v, want ErrInvalidCredentials", err)
		}
	})

	t.Run("disabled user", func(t *testing.T) {
		t.Parallel()
		if _, err := svc.Authenticate("bob", "s3cr3t"); !errors.Is(err, identity.ErrInvalidCredentials) {
			t.Fatalf("err = %v, want ErrInvalidCredentials", err)
		}
	})
}

func TestListRedacts(t *testing.T) {
	t.Parallel()

	svc := newService(t)
	if _, err := svc.Put("user-1", resource.UserSpec{Username: "alice", Password: "s3cr3t"}); err != nil {
		t.Fatalf("put: %v", err)
	}
	users, err := svc.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(users) != 1 || users[0].Status.PasswordHash != nil {
		t.Fatalf("list leaks password material: %+v", users)
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()

	svc := newService(t)
	if _, err := svc.Put("user-1", resource.UserSpec{Username: "alice", Password: "s3cr3t"}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := svc.Delete("user-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get("user-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
