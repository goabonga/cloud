// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package transporttls

import "testing"

func TestEnvironmentRequiresCompleteIdentity(t *testing.T) {
	for _, name := range []string{"GOA_MANAGEMENT_TLS_CA", "GOA_MANAGEMENT_TLS_CERT", "GOA_MANAGEMENT_TLS_KEY"} {
		t.Setenv(name, "")
	}
	cfg, err := FromEnvironment()
	if err != nil || cfg != nil {
		t.Fatalf("empty configuration: %v, %v", cfg, err)
	}
	t.Setenv("GOA_MANAGEMENT_TLS_CA", "missing-ca")
	if _, err := FromEnvironment(); err == nil {
		t.Fatal("partial credentials accepted")
	}
	t.Setenv("GOA_MANAGEMENT_TLS_CERT", "missing-cert")
	t.Setenv("GOA_MANAGEMENT_TLS_KEY", "missing-key")
	if _, err := FromEnvironment(); err == nil {
		t.Fatal("missing credentials accepted")
	}
}
