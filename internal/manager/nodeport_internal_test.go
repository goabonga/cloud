// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import "testing"

func TestNodeAddressCountsDownFromTheLastHost(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		cidr string
		rank int
		want string
	}{
		{"10.20.1.0/24", 0, "10.20.1.254"},
		{"10.20.1.0/24", 1, "10.20.1.253"},
		{"10.20.1.0/24", 4, "10.20.1.250"},
		{"10.20.1.64/26", 0, "10.20.1.126"},
	} {
		got, err := nodeAddress(tc.cidr, tc.rank)
		if err != nil || got != tc.want {
			t.Fatalf("nodeAddress(%s, %d) = %q, %v; want %s", tc.cidr, tc.rank, got, err, tc.want)
		}
	}
	if _, err := nodeAddress("10.20.1.0/24", maxNodePorts); err == nil {
		t.Fatal("a rank past maxNodePorts must have no address")
	}
}

func TestAllocateIPLeavesTheNodeAddressesFree(t *testing.T) {
	t.Parallel()

	// A /26 whose allocator range (.10 upward) reaches the node addresses.
	used := map[string]bool{}
	for {
		ip, err := allocateIP("10.20.1.64/26", used)
		if err != nil {
			break
		}
		used[ip] = true
	}
	for rank := 0; rank < maxNodePorts; rank++ {
		addr, err := nodeAddress("10.20.1.64/26", rank)
		if err != nil {
			t.Fatalf("nodeAddress rank %d: %v", rank, err)
		}
		if used[addr] {
			t.Fatalf("allocateIP handed out node address %s", addr)
		}
	}
	if !used["10.20.1.121"] || used["10.20.1.122"] {
		t.Fatalf("allocation should stop right below the node addresses: %v", used)
	}
}
