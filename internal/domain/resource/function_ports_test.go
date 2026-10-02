// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestUserComputesCannotClaimFunctionPorts(t *testing.T) {
	for _, mapping := range []string{"30000:8080/tcp", "32767:8080"} {
		spec := resource.ComputeSpec{SubnetID: "subnet", Image: "image", Ports: []string{mapping}}
		if err := spec.Validate(); err == nil {
			t.Fatalf("reserved mapping accepted: %s", mapping)
		}
	}
}
