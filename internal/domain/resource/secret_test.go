// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestSecretSpecValidate(t *testing.T) {
	t.Parallel()

	if err := (resource.SecretSpec{Data: "x"}).Validate(); err != nil {
		t.Fatalf("non-empty data should validate: %v", err)
	}
	if err := (resource.SecretSpec{}).Validate(); err == nil {
		t.Fatal("empty data should fail validation")
	}
}

func TestSecretSpecSatisfiesValidator(t *testing.T) {
	t.Parallel()

	var _ resource.Validator = resource.SecretSpec{}
}
