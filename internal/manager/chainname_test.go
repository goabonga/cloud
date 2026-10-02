// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"strings"
	"testing"
)

func TestChainName(t *testing.T) {
	t.Parallel()

	if got := chainName("acl-1"); got != "INFRA-ACL-ACL1" {
		t.Fatalf("chainName(acl-1) = %q", got)
	}
}

func TestChainNameHashesLongUID(t *testing.T) {
	t.Parallel()

	long := "acl-policy-with-a-very-long-identifier-xxxxxxxx"
	got := chainName(long)
	if len(got) > maxChainName {
		t.Fatalf("chainName(%q) = %q (len %d) exceeds %d", long, got, len(got), maxChainName)
	}
	if !strings.HasPrefix(got, "INFRA-ACL-") {
		t.Fatalf("chainName(%q) = %q, want INFRA-ACL- prefix", long, got)
	}
	if again := chainName(long); again != got {
		t.Fatalf("chainName not deterministic: %q vs %q", got, again)
	}
}
