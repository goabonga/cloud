// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestSSLCASpecValidate(t *testing.T) {
	t.Parallel()

	if err := (resource.SSLCASpec{CommonName: "root"}).Validate(); err != nil {
		t.Fatalf("valid spec: %v", err)
	}
	if err := (resource.SSLCASpec{}).Validate(); err == nil {
		t.Fatal("empty common name should fail")
	}
	if err := (resource.SSLCASpec{CommonName: "root", ValidDays: -1}).Validate(); err == nil {
		t.Fatal("negative validDays should fail")
	}
}

func TestSSLCASpecSatisfiesValidator(t *testing.T) {
	t.Parallel()

	var _ resource.Validator = resource.SSLCASpec{}
}
