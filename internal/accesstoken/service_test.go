// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package accesstoken_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/goabonga/infrastructure/internal/accesstoken"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/identity"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func newService(t *testing.T) (*accesstoken.Service, *identity.Service) {
	t.Helper()
	store := state.NewFileStore(t.TempDir())
	users := identity.NewService(registry.New[resource.UserSpec, resource.UserStatus](store, resource.KindUser))
	reg := registry.New[resource.AccessTokenSpec, resource.AccessTokenStatus](store, resource.KindAccessToken)
	return accesstoken.NewService(reg, users), users
}

func TestPutGeneratesAndRedacts(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	out, plaintext, err := svc.Put("tok-1", "user-1", resource.AccessTokenSpec{Name: "ci"})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if plaintext == "" || !strings.HasPrefix(plaintext, "infra_") {
		t.Fatalf("plaintext = %q, want an infra_-prefixed token", plaintext)
	}
	if out.Status.TokenHash != nil {
		t.Fatalf("put response leaks the token hash: %+v", out)
	}
	if !out.Status.IsReady() || out.Metadata.Generation != 1 {
		t.Fatalf("unexpected meta/status: %+v %+v", out.Metadata, out.Status)
	}

	got, err := svc.Get("tok-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.TokenHash != nil {
		t.Fatal("get response leaks the token hash")
	}
}

func TestPutUpdateKeepsExistingToken(t *testing.T) {
	t.Parallel()

	svc, users := newService(t)
	if _, err := users.Put("user-1", resource.UserSpec{Username: "alice", Password: "s3cr3t", Roles: []string{"admin"}}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	_, plaintext, err := svc.Put("tok-1", "user-1", resource.AccessTokenSpec{Name: "ci"})
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	_, second, err := svc.Put("tok-1", "user-1", resource.AccessTokenSpec{Name: "ci-renamed"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if second != "" {
		t.Fatalf("update returned a plaintext %q, want none", second)
	}

	subject, roles, err := svc.Introspect(plaintext)
	if err != nil {
		t.Fatalf("introspect original token after rename: %v", err)
	}
	if subject != "user-1" || len(roles) != 1 || roles[0] != "admin" {
		t.Fatalf("unexpected introspection: subject=%q roles=%v", subject, roles)
	}
}

func TestIntrospect(t *testing.T) {
	t.Parallel()

	svc, users := newService(t)
	if _, err := users.Put("user-1", resource.UserSpec{Username: "alice", Password: "s3cr3t", Roles: []string{"admin"}}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	_, plaintext, err := svc.Put("tok-1", "user-1", resource.AccessTokenSpec{Name: "ci"})
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		subject, roles, err := svc.Introspect(plaintext)
		if err != nil {
			t.Fatalf("introspect: %v", err)
		}
		if subject != "user-1" || len(roles) != 1 || roles[0] != "admin" {
			t.Fatalf("unexpected introspection: subject=%q roles=%v", subject, roles)
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		t.Parallel()
		if _, _, err := svc.Introspect("infra_nope"); !errors.Is(err, accesstoken.ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})

	t.Run("empty token", func(t *testing.T) {
		t.Parallel()
		if _, _, err := svc.Introspect(""); !errors.Is(err, accesstoken.ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})
}

func TestIntrospectExpired(t *testing.T) {
	t.Parallel()

	svc, users := newService(t)
	if _, err := users.Put("user-1", resource.UserSpec{Username: "alice", Password: "s3cr3t"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	past := time.Now().Add(-time.Hour)
	_, plaintext, err := svc.Put("tok-1", "user-1", resource.AccessTokenSpec{Name: "ci", ExpiresAt: &past})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, _, err := svc.Introspect(plaintext); !errors.Is(err, accesstoken.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestIntrospectDisabledOwner(t *testing.T) {
	t.Parallel()

	svc, users := newService(t)
	if _, err := users.Put("user-1", resource.UserSpec{Username: "alice", Password: "s3cr3t"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	_, plaintext, err := svc.Put("tok-1", "user-1", resource.AccessTokenSpec{Name: "ci"})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := users.Put("user-1", resource.UserSpec{Username: "alice", Disabled: true}); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	if _, _, err := svc.Introspect(plaintext); !errors.Is(err, accesstoken.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestIntrospectDeletedOwner(t *testing.T) {
	t.Parallel()

	svc, users := newService(t)
	if _, err := users.Put("user-1", resource.UserSpec{Username: "alice", Password: "s3cr3t"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	_, plaintext, err := svc.Put("tok-1", "user-1", resource.AccessTokenSpec{Name: "ci"})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := users.Delete("user-1"); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if _, _, err := svc.Introspect(plaintext); !errors.Is(err, accesstoken.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	if _, _, err := svc.Put("tok-1", "user-1", resource.AccessTokenSpec{Name: "ci"}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := svc.Delete("tok-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get("tok-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
