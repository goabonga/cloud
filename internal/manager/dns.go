// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/miekg/dns"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
)

// DNSBackend puts the agent's DNS listeners on the host.
type DNSBackend interface {
	// ServeVPC makes the VPC's resolver address, held on its bridge, answer
	// from view. Idempotent: a later call swaps the view.
	ServeVPC(ctx context.Context, vpcID, bridge, addr string, view *DNSView) error
	// StopVPC stops a VPC's resolver. Stopping one not running is a no-op.
	StopVPC(ctx context.Context, vpcID string) error
	// ServedVPCs lists the VPCs whose resolver runs on this host.
	ServedVPCs() []string
	// ServePublic makes addr, held by the host, answer from view.
	ServePublic(ctx context.Context, addr string, view *DNSView) error
}

// NativeDNS is the DNSBackend that serves from the agent process itself, on
// addresses it assigns with iproute2: a VPC's resolver address as a /32 on the
// VPC bridge - on every host, like the subnet gateways, so an instance always
// queries its own host - and the public address as a /32 on the loopback.
type NativeDNS struct {
	run       Runner
	listeners *DNSListeners
	vpcAddrs  map[string]string
}

// NewNativeDNS returns a NativeDNS listening on port 53 and forwarding to the
// host's own resolvers.
func NewNativeDNS() *NativeDNS {
	return NewNativeDNSWith(defaultRun, NewDNSListeners(53, NewUpstreamForwarder()))
}

// NewNativeDNSWith returns a NativeDNS driven by run and listeners, for tests.
func NewNativeDNSWith(run Runner, listeners *DNSListeners) *NativeDNS {
	return &NativeDNS{run: run, listeners: listeners, vpcAddrs: map[string]string{}}
}

// ServeVPC implements DNSBackend.
func (d *NativeDNS) ServeVPC(ctx context.Context, vpcID, bridge, addr string, view *DNSView) error {
	if out, err := d.run(ctx, "ip", "addr", "replace", addr+"/32", "dev", bridge); err != nil {
		return fmt.Errorf("manager: add dns address %s on %s: %w: %s", addr, bridge, err, strings.TrimSpace(out))
	}
	if err := d.listeners.Serve(addr, view); err != nil {
		return err
	}
	d.vpcAddrs[vpcID] = addr
	return nil
}

// StopVPC implements DNSBackend. The address goes with the VPC's bridge.
func (d *NativeDNS) StopVPC(_ context.Context, vpcID string) error {
	if addr, ok := d.vpcAddrs[vpcID]; ok {
		d.listeners.Stop(addr)
		delete(d.vpcAddrs, vpcID)
	}
	return nil
}

// ServedVPCs implements DNSBackend.
func (d *NativeDNS) ServedVPCs() []string {
	out := make([]string, 0, len(d.vpcAddrs))
	for id := range d.vpcAddrs {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ServePublic implements DNSBackend.
func (d *NativeDNS) ServePublic(ctx context.Context, addr string, view *DNSView) error {
	if out, err := d.run(ctx, "ip", "addr", "replace", addr+"/32", "dev", "lo"); err != nil {
		return fmt.Errorf("manager: add public dns address %s: %w: %s", addr, err, strings.TrimSpace(out))
	}
	return d.listeners.Serve(addr, view)
}

// DNSZoneRegistry is the typed store of DNS zones.
type DNSZoneRegistry = registry.Registry[resource.DNSZoneSpec, resource.DNSZoneStatus]

// DNSRecordRegistry is the typed store of DNS records.
type DNSRecordRegistry = registry.Registry[resource.DNSRecordSpec, resource.DNSRecordStatus]

// DNSReconciler serves DNS from the agent. Every VPC gets a resolver on its
// first address - the address its instances are handed as their nameserver -
// that answers authoritatively for the private zones attached to the VPC and
// for every public zone, and forwards any other name to the host's resolvers.
// With a public address configured, the host also answers every public zone
// there, authoritatively and nothing else. Views are rebuilt from the store
// every pass, so a zone or record change (neither carries a finalizer) is
// served on the next tick.
type DNSReconciler struct {
	zones      *DNSZoneRegistry
	records    *DNSRecordRegistry
	vpcs       *VPCRegistry
	backend    DNSBackend
	publicAddr string
}

// NewDNSReconciler returns a DNS pass backed by backend.
func NewDNSReconciler(zones *DNSZoneRegistry, records *DNSRecordRegistry, vpcs *VPCRegistry, backend DNSBackend) *DNSReconciler {
	return &DNSReconciler{zones: zones, records: records, vpcs: vpcs, backend: backend}
}

// WithPublicAddress makes the host answer the public zones on addr, e.g. an
// address of the public block routed to the edges.
func (r *DNSReconciler) WithPublicAddress(addr string) *DNSReconciler {
	r.publicAddr = addr
	return r
}

// Name identifies the reconcile pass.
func (r *DNSReconciler) Name() string { return resource.KindDNSZone }

// ReconcileAll rebuilds every view from the store and serves it.
func (r *DNSReconciler) ReconcileAll(ctx context.Context) error {
	vpcs, err := r.vpcs.List()
	if err != nil {
		return fmt.Errorf("manager: list vpcs: %w", err)
	}
	zones, err := r.zones.List()
	if err != nil {
		return fmt.Errorf("manager: list dns zones: %w", err)
	}
	records, err := r.records.List()
	if err != nil {
		return fmt.Errorf("manager: list dns records: %w", err)
	}

	built, badRecords := buildZones(zones, records)
	var public []DNSZone
	for i := range zones {
		if z := &zones[i]; !z.Metadata.IsDeleting() && z.Spec.Visibility == "public" {
			public = append(public, built[z.Metadata.UID])
		}
	}

	var errs []error
	served := map[string]bool{}
	for i := range vpcs {
		v := &vpcs[i]
		if v.Metadata.IsDeleting() || v.Status.BridgeName == "" {
			continue
		}
		view := &DNSView{Zones: append(privateZonesOf(v.Metadata.UID, zones, built), public...), Forward: true}
		if err := r.backend.ServeVPC(ctx, v.Metadata.UID, v.Status.BridgeName, firstHostOf(v.Spec.CIDR), view); err != nil {
			errs = append(errs, fmt.Errorf("vpc %s dns: %w", v.Metadata.UID, err))
			continue
		}
		served[v.Metadata.UID] = true
	}
	for _, id := range r.backend.ServedVPCs() {
		if !served[id] {
			if err := r.backend.StopVPC(ctx, id); err != nil {
				errs = append(errs, err)
			}
		}
	}
	publicServed := false
	if r.publicAddr != "" {
		if err := r.backend.ServePublic(ctx, r.publicAddr, &DNSView{Zones: public}); err != nil {
			errs = append(errs, fmt.Errorf("public dns: %w", err))
		} else {
			publicServed = true
		}
	}

	r.updateStatuses(zones, records, served, publicServed, badRecords)
	return errors.Join(errs...)
}

// buildZones parses every live record into its zone. A record with a value
// that does not parse is left out whole and reported.
func buildZones(zones []resource.DNSZone, records []resource.DNSRecord) (map[string]DNSZone, map[string]error) {
	built := make(map[string]DNSZone, len(zones))
	for i := range zones {
		if z := &zones[i]; !z.Metadata.IsDeleting() {
			built[z.Metadata.UID] = DNSZone{Domain: z.Spec.Domain}
		}
	}
	bad := map[string]error{}
	for i := range records {
		rec := &records[i]
		zone, ok := built[rec.Spec.ZoneID]
		if rec.Metadata.IsDeleting() || !ok {
			continue
		}
		rrs := make([]dns.RR, 0, len(rec.Spec.Records))
		for _, val := range rec.Spec.Records {
			rr, err := ParseRecord(rec.Spec.Name, zone.Domain, rec.Spec.Type, rec.Spec.TTL, val)
			if err != nil {
				bad[rec.Metadata.UID] = err
				rrs = nil
				break
			}
			rrs = append(rrs, rr)
		}
		zone.Records = append(zone.Records, rrs...)
		built[rec.Spec.ZoneID] = zone
	}
	return built, bad
}

// privateZonesOf returns the private zones attached to vpcID.
func privateZonesOf(vpcID string, zones []resource.DNSZone, built map[string]DNSZone) []DNSZone {
	var out []DNSZone
	for i := range zones {
		z := &zones[i]
		if z.Metadata.IsDeleting() || z.Spec.Visibility == "public" {
			continue
		}
		for _, id := range z.Spec.VPCIDs {
			if id == vpcID {
				out = append(out, built[z.Metadata.UID])
				break
			}
		}
	}
	return out
}

// updateStatuses records each zone's and record's phase, writing only on change.
func (r *DNSReconciler) updateStatuses(zones []resource.DNSZone, records []resource.DNSRecord, served map[string]bool, publicServed bool, bad map[string]error) {
	zoneReady := map[string]bool{}
	for i := range zones {
		z := &zones[i]
		if z.Metadata.IsDeleting() {
			continue
		}
		phase, reason, msg := zonePhaseFor(z, served, publicServed)
		zoneReady[z.Metadata.UID] = phase == resource.PhaseReady
		if z.Status.Phase == phase {
			continue
		}
		z.Status.SetPhase(phase, reason, msg)
		if phase == resource.PhaseReady {
			z.Status.MarkReconciled(z.Metadata.Generation)
		}
		_ = r.zones.Put(z)
	}
	for i := range records {
		rec := &records[i]
		if rec.Metadata.IsDeleting() {
			continue
		}
		phase, reason, msg := resource.PhasePending, "WaitingForZone", "zone not served yet"
		switch {
		case bad[rec.Metadata.UID] != nil:
			phase, reason, msg = resource.PhaseError, "BadRecord", bad[rec.Metadata.UID].Error()
		case zoneReady[rec.Spec.ZoneID]:
			phase, reason, msg = resource.PhaseReady, "Served", "record served"
		}
		if rec.Status.Phase == phase {
			continue
		}
		rec.Status.SetPhase(phase, reason, msg)
		if phase == resource.PhaseReady {
			rec.Status.MarkReconciled(rec.Metadata.Generation)
		}
		_ = r.records.Put(rec)
	}
}

// zonePhaseFor derives a zone's phase: a private zone is served once every
// VPC it is attached to has a resolver on this host; a public zone once the
// public listener or some VPC resolver serves it.
func zonePhaseFor(z *resource.DNSZone, served map[string]bool, publicServed bool) (resource.Phase, string, string) {
	if z.Spec.Visibility == "public" {
		if publicServed || len(served) > 0 {
			return resource.PhaseReady, "Served", "public zone served"
		}
		return resource.PhasePending, "WaitingForResolver", "no resolver serves it yet"
	}
	if len(z.Spec.VPCIDs) == 0 {
		return resource.PhasePending, "NoVPC", "private zone attached to no VPC"
	}
	for _, vpcID := range z.Spec.VPCIDs {
		if !served[vpcID] {
			return resource.PhasePending, "WaitingForVPC", "vpc resolver not ready"
		}
	}
	return resource.PhaseReady, "Served", "zone served"
}

// recordFQDN joins a record name and zone domain into a fully qualified name.
func recordFQDN(name, domain string) string {
	switch {
	case name == "" || name == "@":
		return domain
	case strings.HasSuffix(name, "."):
		return strings.TrimSuffix(name, ".")
	default:
		return name + "." + domain
	}
}
