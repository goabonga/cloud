// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"strings"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// PeeringBackend abstracts the veth operations that link two VPC bridges.
type PeeringBackend interface {
	// EnsureLink creates a veth pair joining bridge1 and bridge2 if absent and
	// brings it up. Idempotent.
	EnsureLink(ctx context.Context, veth1, veth2, bridge1, bridge2 string) error
	// DeleteLink removes the veth pair (deleting one end removes both). Removing
	// an absent link is not an error.
	DeleteLink(ctx context.Context, veth1 string) error
	// EnsureRoutes routes each VPC's CIDR from the other's table onto its
	// bridge, as each VPC routes in a VRF of its own (see vrf.go). Idempotent.
	EnsureRoutes(ctx context.Context, a, b PeeringSide) error
	// DeleteRoutes removes those routes. Removing absent routes is not an
	// error.
	DeleteRoutes(ctx context.Context, a, b PeeringSide) error
}

// PeeringSide is one VPC of a peering, as its routes need it.
type PeeringSide struct {
	VPCID  string
	CIDR   string
	Bridge string
}

// peeringNames derives the two veth interface names for a peering UID, within
// the kernel interface-name limit.
func peeringNames(uid string) (veth1, veth2 string) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(uid))
	s := fmt.Sprintf("%08x", h.Sum32())
	return "pa-" + s, "pb-" + s
}

// ExecPeering is a PeeringBackend that shells out to iproute2 (`ip`). It
// requires root / CAP_NET_ADMIN at run time.
type ExecPeering struct {
	run Runner
}

// NewExecPeering returns an ExecPeering using the real `ip` command.
func NewExecPeering() *ExecPeering {
	return &ExecPeering{run: defaultRun}
}

// NewExecPeeringWithRunner returns a backend driven by a custom runner, used in
// tests to assert the issued commands without touching the kernel.
func NewExecPeeringWithRunner(run Runner) *ExecPeering {
	return &ExecPeering{run: run}
}

// linkExists reports whether the named interface is present.
func (p *ExecPeering) linkExists(ctx context.Context, name string) (bool, error) {
	out, err := p.run(ctx, "ip", "link", "show", name)
	if err == nil {
		return true, nil
	}
	if strings.Contains(out, "does not exist") || strings.Contains(out, "Cannot find device") {
		return false, nil
	}
	return false, fmt.Errorf("manager: link exists %q: %w: %s", name, err, strings.TrimSpace(out))
}

// EnsureLink creates and attaches the veth pair if the first end is absent.
func (p *ExecPeering) EnsureLink(ctx context.Context, veth1, veth2, bridge1, bridge2 string) error {
	exists, err := p.linkExists(ctx, veth1)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	steps := [][]string{
		{"link", "add", veth1, "type", "veth", "peer", "name", veth2},
		{"link", "set", veth1, "master", bridge1},
		{"link", "set", veth1, "up"},
		{"link", "set", veth2, "master", bridge2},
		{"link", "set", veth2, "up"},
	}
	for _, s := range steps {
		if out, err := p.run(ctx, "ip", s...); err != nil {
			return fmt.Errorf("manager: peering link %v: %w: %s", s, err, strings.TrimSpace(out))
		}
	}
	return nil
}

// EnsureRoutes adds each side's CIDR to the other side's table, onto its own
// bridge.
func (p *ExecPeering) EnsureRoutes(ctx context.Context, a, b PeeringSide) error {
	for _, r := range [][2]PeeringSide{{a, b}, {b, a}} {
		from, to := r[0], r[1]
		if out, err := p.run(ctx, "ip", "route", "replace", to.CIDR, "dev", to.Bridge, "table", vrfTableArg(from.VPCID)); err != nil {
			return fmt.Errorf("manager: peering route %s in vpc %s: %w: %s", to.CIDR, from.VPCID, err, strings.TrimSpace(out))
		}
	}
	return nil
}

// DeleteRoutes removes the routes EnsureRoutes added (best effort).
func (p *ExecPeering) DeleteRoutes(ctx context.Context, a, b PeeringSide) error {
	for _, r := range [][2]PeeringSide{{a, b}, {b, a}} {
		from, to := r[0], r[1]
		_, _ = p.run(ctx, "ip", "route", "del", to.CIDR, "dev", to.Bridge, "table", vrfTableArg(from.VPCID))
	}
	return nil
}

// DeleteLink removes the veth pair by deleting its first end.
func (p *ExecPeering) DeleteLink(ctx context.Context, veth1 string) error {
	out, err := p.run(ctx, "ip", "link", "del", veth1)
	if err != nil && !strings.Contains(out, "does not exist") && !strings.Contains(out, "Cannot find device") {
		return fmt.Errorf("manager: delete peering link %q: %w: %s", veth1, err, strings.TrimSpace(out))
	}
	return nil
}

// PeeringRegistry is the typed store the peering reconciler reads and writes.
type PeeringRegistry = registry.Registry[resource.PeeringSpec, resource.PeeringStatus]

// PeeringReconciler realizes a peering by linking the two VPC bridges with a
// veth pair and routing each VPC's CIDR from the other's table. Two VPCs whose
// CIDRs overlap cannot be peered.
type PeeringReconciler struct {
	reg     *PeeringRegistry
	vpcs    *VPCRegistry
	backend PeeringBackend
}

// NewPeeringReconciler returns a reconciler backed by reg, the VPC store and the
// peering backend.
func NewPeeringReconciler(reg *PeeringRegistry, vpcs *VPCRegistry, backend PeeringBackend) *PeeringReconciler {
	return &PeeringReconciler{reg: reg, vpcs: vpcs, backend: backend}
}

// Name identifies the reconcile pass.
func (r *PeeringReconciler) Name() string { return resource.KindPeering }

// ReconcileAll reconciles every peering, collecting per-peering errors.
func (r *PeeringReconciler) ReconcileAll(ctx context.Context) error {
	peerings, err := r.reg.WithContext(ctx).List()
	if err != nil {
		return fmt.Errorf("manager: list peerings: %w", err)
	}
	var errs []error
	for i := range peerings {
		uid := peerings[i].Metadata.UID
		if err := r.Reconcile(ctx, uid); err != nil {
			errs = append(errs, fmt.Errorf("peering %s: %w", uid, err))
		}
	}
	return errors.Join(errs...)
}

// Reconcile brings the peering identified by uid in line with its spec.
func (r *PeeringReconciler) Reconcile(ctx context.Context, uid string) error {
	pr, err := r.reg.WithContext(ctx).Get(uid)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("manager: load peering %q: %w", uid, err)
	}
	if pr.Metadata.IsDeleting() {
		return r.finalize(ctx, pr)
	}
	return r.ensure(ctx, pr)
}

func (r *PeeringReconciler) ensure(ctx context.Context, pr *resource.Peering) error {
	if !pr.Metadata.HasFinalizer(resource.PeeringFinalizer) {
		pr.Metadata.AddFinalizer(resource.PeeringFinalizer)
	}

	side1, ok1, err := r.side(pr.Spec.VPC1ID)
	if err != nil {
		pr.Status.SetPhase(resource.PhaseError, "VPCError", err.Error())
		_ = r.reg.WithContext(ctx).Put(pr)
		return err
	}
	side2, ok2, err := r.side(pr.Spec.VPC2ID)
	if err != nil {
		pr.Status.SetPhase(resource.PhaseError, "VPCError", err.Error())
		_ = r.reg.WithContext(ctx).Put(pr)
		return err
	}
	if cidrsOverlap(side1.CIDR, side2.CIDR) {
		err := fmt.Errorf("vpc %s (%s) and vpc %s (%s) overlap", side1.VPCID, side1.CIDR, side2.VPCID, side2.CIDR)
		pr.Status.SetPhase(resource.PhaseError, "Overlap", err.Error())
		_ = r.reg.WithContext(ctx).Put(pr)
		return err
	}
	if !ok1 || !ok2 {
		// A VPC bridge is not provisioned yet; retry on the next pass.
		pr.Status.SetPhase(resource.PhasePending, "WaitingForVPC", "vpc bridge not ready")
		return r.reg.WithContext(ctx).Put(pr)
	}

	veth1, veth2 := peeringNames(pr.Metadata.UID)
	pr.Status.SetPhase(resource.PhaseReconciling, "Reconciling", "linking bridges")
	if err := r.backend.EnsureLink(ctx, veth1, veth2, side1.Bridge, side2.Bridge); err != nil {
		pr.Status.SetPhase(resource.PhaseError, "LinkError", err.Error())
		_ = r.reg.WithContext(ctx).Put(pr)
		return err
	}
	if err := r.backend.EnsureRoutes(ctx, side1, side2); err != nil {
		pr.Status.SetPhase(resource.PhaseError, "RouteError", err.Error())
		_ = r.reg.WithContext(ctx).Put(pr)
		return err
	}

	pr.Status.Veth1 = veth1
	pr.Status.Veth2 = veth2
	pr.Status.MarkReconciled(pr.Metadata.Generation)
	pr.Status.SetPhase(resource.PhaseReady, "Linked", "bridges linked")
	if err := r.reg.WithContext(ctx).Put(pr); err != nil {
		return fmt.Errorf("manager: save peering %q: %w", pr.Metadata.UID, err)
	}
	return nil
}

func (r *PeeringReconciler) finalize(ctx context.Context, pr *resource.Peering) error {
	if pr.Metadata.HasFinalizer(resource.PeeringFinalizer) {
		side1, ok1, _ := r.side(pr.Spec.VPC1ID)
		side2, ok2, _ := r.side(pr.Spec.VPC2ID)
		if ok1 && ok2 {
			_ = r.backend.DeleteRoutes(ctx, side1, side2)
		}
		veth1, _ := peeringNames(pr.Metadata.UID)
		if err := r.backend.DeleteLink(ctx, veth1); err != nil {
			pr.Status.SetPhase(resource.PhaseError, "LinkError", err.Error())
			_ = r.reg.WithContext(ctx).Put(pr)
			return err
		}
		pr.Metadata.RemoveFinalizer(resource.PeeringFinalizer)
		pr.Status.SetPhase(resource.PhaseDeleting, "Deleting", "link removed")
		if err := r.reg.WithContext(ctx).Put(pr); err != nil {
			return fmt.Errorf("manager: save peering %q: %w", pr.Metadata.UID, err)
		}
	}
	if len(pr.Metadata.Finalizers) == 0 {
		if err := r.reg.WithContext(ctx).Delete(pr.Metadata.UID); err != nil {
			return fmt.Errorf("manager: delete peering %q: %w", pr.Metadata.UID, err)
		}
	}
	return nil
}

// side returns a VPC as a side of the peering and whether its bridge is
// provisioned.
func (r *PeeringReconciler) side(vpcID string) (PeeringSide, bool, error) {
	vpc, err := r.vpcs.Get(vpcID)
	if errors.Is(err, state.ErrNotFound) {
		return PeeringSide{}, false, fmt.Errorf("vpc %q not found", vpcID)
	}
	if err != nil {
		return PeeringSide{}, false, err
	}
	s := PeeringSide{VPCID: vpcID, CIDR: vpc.Spec.CIDR, Bridge: vpc.Status.BridgeName}
	return s, s.Bridge != "", nil
}

// cidrsOverlap reports whether two CIDRs share an address. An unparsable one
// overlaps nothing: the VPC's own validation reports it.
func cidrsOverlap(a, b string) bool {
	_, na, errA := net.ParseCIDR(a)
	_, nb, errB := net.ParseCIDR(b)
	if errA != nil || errB != nil {
		return false
	}
	return na.Contains(nb.IP) || nb.Contains(na.IP)
}
