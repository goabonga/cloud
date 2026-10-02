// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package state_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/state"
)

func TestEtcdRejectsRemotePlaintext(t *testing.T) {
	for _, name := range []string{"GOA_MANAGEMENT_TLS_CA", "GOA_MANAGEMENT_TLS_CERT", "GOA_MANAGEMENT_TLS_KEY"} {
		t.Setenv(name, "")
	}
	for _, endpoint := range []string{"192.0.2.1:2379", "http://etcd.example:2379", "https://127.0.0.1:2379", "ftp://127.0.0.1:2379"} {
		if s, err := state.NewEtcdStore([]string{endpoint}); err == nil {
			_ = s.Close()
			t.Fatalf("accepted insecure endpoint %q", endpoint)
		}
	}
}
