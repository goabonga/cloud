// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path"
	"sort"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// IPAddressRegistry is the typed store of reserved addresses.
type IPAddressRegistry = registry.Registry[resource.IPAddressSpec, resource.IPAddressStatus]

// publicPool is the reservation namespace of the edges' public block.
const publicPool = "public"

// PublicIPReconciler resolves public ip_address resources on an edge - a host
// configured with the public block routed to the edges. Each gets an address
// of that block, the one it requests or else the first free one, reserved in
// the shared store so every edge, reconciling at once, settles on the same
// address. Hosts without a public block leave them alone. ip_address carries
// no finalizer, so reservations whose ip_address is gone are released here.
type PublicIPReconciler struct {
	reg      *IPAddressRegistry
	store    state.Store
	book     *addressBook
	cidr     string
	reserved map[string]bool
}

// NewPublicIPReconciler returns a public address pass over the block cidr (no
// pass when empty). reserved lists addresses of the block that are taken for
// another purpose, such as the public DNS address.
func NewPublicIPReconciler(reg *IPAddressRegistry, store state.Store, cidr string, reserved ...string) *PublicIPReconciler {
	r := &PublicIPReconciler{reg: reg, store: store, book: &addressBook{store: store}, cidr: cidr, reserved: map[string]bool{}}
	for _, a := range reserved {
		if a != "" {
			r.reserved[a] = true
		}
	}
	return r
}

// Name identifies the reconcile pass.
func (r *PublicIPReconciler) Name() string { return resource.KindIPAddress }

// ReconcileAll resolves every public ip_address and releases stale
// reservations.
func (r *PublicIPReconciler) ReconcileAll(_ context.Context) error {
	if r.cidr == "" {
		return nil
	}
	ips, err := r.reg.List()
	if err != nil {
		return fmt.Errorf("manager: list ip addresses: %w", err)
	}
	var errs []error
	live := map[string]bool{}
	requested := map[string]bool{}
	var public []*resource.IPAddress
	for i := range ips {
		if ip := &ips[i]; !ip.Metadata.IsDeleting() && ip.Spec.Type == "public" {
			live[ip.Metadata.UID] = true
			public = append(public, ip)
			if ip.Spec.Address != "" {
				requested[ip.Spec.Address] = true
			}
		}
	}
	// Addresses asked for by name go first, and are never handed out to an
	// ip_address that asks for none.
	sort.SliceStable(public, func(i, j int) bool { return public[i].Spec.Address != "" && public[j].Spec.Address == "" })
	for _, ip := range public {
		addr, err := r.resolve(ip, requested)
		if err != nil {
			errs = append(errs, fmt.Errorf("ip address %s: %w", ip.Metadata.UID, err))
			if ip.Status.Phase != resource.PhaseError {
				ip.Status.SetPhase(resource.PhaseError, "AddressError", err.Error())
				_ = r.reg.Put(ip)
			}
			continue
		}
		if ip.Status.Address == addr && ip.Status.IsConverged(ip.Metadata.Generation) {
			continue
		}
		ip.Status.Address = addr
		ip.Status.MarkReconciled(ip.Metadata.Generation)
		ip.Status.SetPhase(resource.PhaseReady, "Reserved", "public address reserved")
		if err := r.reg.Put(ip); err != nil {
			errs = append(errs, fmt.Errorf("manager: save ip address %q: %w", ip.Metadata.UID, err))
		}
	}
	if err := r.releaseStale(live); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// resolve returns the address ip holds, reserving it first. requested lists
// the addresses other ip_addresses ask for by name.
func (r *PublicIPReconciler) resolve(ip *resource.IPAddress, requested map[string]bool) (string, error) {
	_, block, err := net.ParseCIDR(r.cidr)
	if err != nil {
		return "", fmt.Errorf("manager: public block %q: %w", r.cidr, err)
	}
	want := ip.Spec.Address
	if want == "" {
		want = ip.Status.Address
	}
	if want == "" {
		used := map[string]bool{}
		for a := range r.reserved {
			used[a] = true
		}
		for a := range requested {
			used[a] = true
		}
		return r.book.allocate(r.cidr, publicPool, ip.Metadata.UID, used)
	}
	if parsed := net.ParseIP(want); parsed == nil || !block.Contains(parsed) {
		return "", fmt.Errorf("address %s is outside the public block %s", want, r.cidr)
	}
	if r.reserved[want] {
		return "", fmt.Errorf("address %s is reserved for another use", want)
	}
	ok, owner, err := r.book.claim(publicPool, want, ip.Metadata.UID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("address %s is reserved by %s", want, owner)
	}
	return want, nil
}

// releaseStale drops the public reservations of ip_addresses that are gone.
func (r *PublicIPReconciler) releaseStale(live map[string]bool) error {
	kvs, err := r.store.List(path.Join("ipam", publicPool))
	if err != nil {
		return fmt.Errorf("manager: list public reservations: %w", err)
	}
	for _, kv := range kvs {
		if owner := string(kv.Value); !live[owner] {
			if err := r.book.release(publicPool, path.Base(kv.Key), owner); err != nil {
				return err
			}
		}
	}
	return nil
}
