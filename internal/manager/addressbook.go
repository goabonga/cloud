// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"errors"
	"fmt"

	"github.com/goabonga/infrastructure/internal/state"
)

// addressBook reserves compute addresses in the shared store. Every agent
// allocates the addresses of the instances scheduled onto its own host, and
// reading which addresses are taken before writing one is not enough: two
// agents allocating at once both see the same address free. A reservation is
// a key created only if absent (compare-and-swap against "absent"), so exactly
// one instance wins each address.
type addressBook struct {
	store state.Store
}

// addressKey is the reservation of ip in subnet.
func addressKey(subnet, ip string) string {
	return "ipam/" + subnet + "/" + ip
}

// claim reserves ip in subnet for owner. It reports whether owner holds the
// reservation afterwards, and who does when it does not.
func (b *addressBook) claim(subnet, ip, owner string) (bool, string, error) {
	key := addressKey(subnet, ip)
	ok, err := b.store.CompareAndSwap(key, nil, []byte(owner))
	if err != nil {
		return false, "", fmt.Errorf("manager: reserve %s in %s: %w", ip, subnet, err)
	}
	if ok {
		return true, owner, nil
	}
	cur, err := b.store.Get(key)
	if err != nil {
		return false, "", fmt.Errorf("manager: read reservation of %s in %s: %w", ip, subnet, err)
	}
	return string(cur) == owner, string(cur), nil
}

// allocate reserves the first address of cidr that is neither in used nor
// reserved by another instance.
func (b *addressBook) allocate(cidr, subnet, owner string, used map[string]bool) (string, error) {
	for {
		ip, err := allocateIP(cidr, used)
		if err != nil {
			return "", err
		}
		ok, _, err := b.claim(subnet, ip, owner)
		if err != nil {
			return "", err
		}
		if ok {
			return ip, nil
		}
		used[ip] = true
	}
}

// release drops owner's reservation of ip in subnet; another owner's is kept.
func (b *addressBook) release(subnet, ip, owner string) error {
	key := addressKey(subnet, ip)
	cur, err := b.store.Get(key)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("manager: read reservation of %s in %s: %w", ip, subnet, err)
	}
	if string(cur) != owner {
		return nil
	}
	if err := b.store.Delete(key); err != nil {
		return fmt.Errorf("manager: release %s in %s: %w", ip, subnet, err)
	}
	return nil
}
