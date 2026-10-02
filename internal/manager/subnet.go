// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// SubnetRegistry is the typed store the subnet reconciler reads and writes.
type SubnetRegistry = registry.Registry[resource.SubnetSpec, resource.SubnetStatus]

// SubnetReconciler realizes a subnet by assigning its gateway address to the
// parent VPC's bridge, and this host's address in it to the node port of the
// VPC's load-balancer namespace.
type SubnetReconciler struct {
	reg      *SubnetRegistry
	vpcs     *VPCRegistry
	net      NetworkBackend
	nodes    *NodeRegistry
	nodeName string
}

// NewSubnetReconciler returns a reconciler backed by reg, the VPC store and net.
func NewSubnetReconciler(reg *SubnetRegistry, vpcs *VPCRegistry, net NetworkBackend) *SubnetReconciler {
	return &SubnetReconciler{reg: reg, vpcs: vpcs, net: net}
}

// WithNodeIdentity tells the reconciler which registered node it runs on, so
// the host takes its own rank's address on the node port. Without it, or before
// the node is registered, the host takes rank 0: right for a single host.
func (r *SubnetReconciler) WithNodeIdentity(nodes *NodeRegistry, nodeName string) *SubnetReconciler {
	r.nodes, r.nodeName = nodes, nodeName
	return r
}

// Name identifies the reconcile pass.
func (r *SubnetReconciler) Name() string { return resource.KindSubnet }

// nodeRank is this host's position among the registered nodes, by UID.
func (r *SubnetReconciler) nodeRank() (int, error) {
	if r.nodes == nil || r.nodeName == "" {
		return 0, nil
	}
	nodes, err := r.nodes.List()
	if err != nil {
		return 0, fmt.Errorf("manager: list nodes: %w", err)
	}
	uids := make([]string, 0, len(nodes))
	for i := range nodes {
		uids = append(uids, nodes[i].Metadata.UID)
	}
	slices.Sort(uids)
	if i := slices.Index(uids, r.nodeName); i >= 0 {
		return i, nil
	}
	return 0, nil
}

// syncNodeAddress puts this host's address for cidr on the node port of the
// VPC's load-balancer namespace and removes the other ranks' addresses, which
// a change in the node set can leave behind.
func (r *SubnetReconciler) syncNodeAddress(ctx context.Context, vpcID, cidr string) error {
	rank, err := r.nodeRank()
	if err != nil {
		return err
	}
	prefix := "/" + strconv.Itoa(prefixLen(cidr))
	for i := 0; i < maxNodePorts; i++ {
		addr, aErr := nodeAddress(cidr, i)
		if aErr != nil {
			if i == rank {
				return aErr
			}
			continue
		}
		if i == rank {
			err = r.net.EnsureNodeAddress(ctx, vpcID, addr+prefix)
		} else {
			err = r.net.DeleteNodeAddress(ctx, vpcID, addr+prefix)
		}
		if err != nil {
			return err
		}
	}
	if rank >= maxNodePorts {
		_, err = nodeAddress(cidr, rank)
		return err
	}
	return nil
}

// ReconcileAll reconciles every subnet, collecting per-subnet errors.
func (r *SubnetReconciler) ReconcileAll(ctx context.Context) error {
	subnets, err := r.reg.WithContext(ctx).List()
	if err != nil {
		return fmt.Errorf("manager: list subnets: %w", err)
	}
	var errs []error
	for i := range subnets {
		uid := subnets[i].Metadata.UID
		if err := r.Reconcile(ctx, uid); err != nil {
			errs = append(errs, fmt.Errorf("subnet %s: %w", uid, err))
		}
	}
	return errors.Join(errs...)
}

// Reconcile brings the subnet identified by uid in line with its spec.
func (r *SubnetReconciler) Reconcile(ctx context.Context, uid string) error {
	sn, err := r.reg.WithContext(ctx).Get(uid)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("manager: load subnet %q: %w", uid, err)
	}
	if sn.Metadata.IsDeleting() {
		return r.finalize(ctx, sn)
	}
	return r.ensure(ctx, sn)
}

func (r *SubnetReconciler) ensure(ctx context.Context, sn *resource.Subnet) error {
	if !sn.Metadata.HasFinalizer(resource.SubnetFinalizer) {
		sn.Metadata.AddFinalizer(resource.SubnetFinalizer)
	}

	bridge, ok, err := r.vpcBridge(sn.Spec.VPCID)
	if err != nil {
		sn.Status.SetPhase(resource.PhaseError, "VPCError", err.Error())
		_ = r.reg.WithContext(ctx).Put(sn)
		return err
	}
	if !ok {
		// The VPC bridge is not provisioned yet; retry on the next pass.
		sn.Status.SetPhase(resource.PhasePending, "WaitingForVPC", "vpc bridge not ready")
		return r.reg.WithContext(ctx).Put(sn)
	}

	gwCIDR, err := gatewayCIDR(sn.Spec.CIDR)
	if err != nil {
		sn.Status.SetPhase(resource.PhaseError, "BadCIDR", err.Error())
		_ = r.reg.WithContext(ctx).Put(sn)
		return err
	}
	sn.Status.SetPhase(resource.PhaseReconciling, "Reconciling", "assigning gateway")
	if err := r.net.EnsureGatewayAddress(ctx, bridge, gwCIDR); err != nil {
		sn.Status.SetPhase(resource.PhaseError, "AddressError", err.Error())
		_ = r.reg.WithContext(ctx).Put(sn)
		return err
	}
	if err := r.syncNodeAddress(ctx, sn.Spec.VPCID, sn.Spec.CIDR); err != nil {
		sn.Status.SetPhase(resource.PhaseError, "NodeAddressError", err.Error())
		_ = r.reg.WithContext(ctx).Put(sn)
		return err
	}

	sn.Status.Gateway = hostOf(gwCIDR)
	sn.Status.MarkReconciled(sn.Metadata.Generation)
	sn.Status.SetPhase(resource.PhaseReady, "Reconciled", "gateway assigned")
	if err := r.reg.WithContext(ctx).Put(sn); err != nil {
		return fmt.Errorf("manager: save subnet %q: %w", sn.Metadata.UID, err)
	}
	return nil
}

func (r *SubnetReconciler) finalize(ctx context.Context, sn *resource.Subnet) error {
	if sn.Metadata.HasFinalizer(resource.SubnetFinalizer) {
		if bridge, ok, err := r.vpcBridge(sn.Spec.VPCID); err == nil && ok {
			prefix := "/" + strconv.Itoa(prefixLen(sn.Spec.CIDR))
			for i := 0; i < maxNodePorts; i++ {
				if addr, aErr := nodeAddress(sn.Spec.CIDR, i); aErr == nil {
					_ = r.net.DeleteNodeAddress(ctx, sn.Spec.VPCID, addr+prefix)
				}
			}
			if gwCIDR, gwErr := gatewayCIDR(sn.Spec.CIDR); gwErr == nil {
				if err := r.net.DeleteAddress(ctx, bridge, gwCIDR); err != nil {
					sn.Status.SetPhase(resource.PhaseError, "AddressError", err.Error())
					_ = r.reg.WithContext(ctx).Put(sn)
					return err
				}
			}
		}
		sn.Metadata.RemoveFinalizer(resource.SubnetFinalizer)
		sn.Status.SetPhase(resource.PhaseDeleting, "Deleting", "gateway removed")
		if err := r.reg.WithContext(ctx).Put(sn); err != nil {
			return fmt.Errorf("manager: save subnet %q: %w", sn.Metadata.UID, err)
		}
	}
	if len(sn.Metadata.Finalizers) == 0 {
		if err := r.reg.WithContext(ctx).Delete(sn.Metadata.UID); err != nil {
			return fmt.Errorf("manager: delete subnet %q: %w", sn.Metadata.UID, err)
		}
	}
	return nil
}

// vpcBridge returns the bridge name of a VPC and whether it is provisioned.
func (r *SubnetReconciler) vpcBridge(vpcID string) (string, bool, error) {
	vpc, err := r.vpcs.Get(vpcID)
	if errors.Is(err, state.ErrNotFound) {
		return "", false, fmt.Errorf("vpc %q not found", vpcID)
	}
	if err != nil {
		return "", false, err
	}
	if vpc.Status.BridgeName == "" {
		return "", false, nil
	}
	return vpc.Status.BridgeName, true, nil
}

// gatewayCIDR returns the first usable address of cidr in CIDR form, e.g.
// "10.0.1.0/24" -> "10.0.1.1/24".
func gatewayCIDR(cidr string) (string, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("invalid cidr %q: %w", cidr, err)
	}
	gw := make(net.IP, len(ipnet.IP))
	copy(gw, ipnet.IP)
	gw[len(gw)-1]++
	prefix, _ := ipnet.Mask.Size()
	return fmt.Sprintf("%s/%d", gw.String(), prefix), nil
}

// hostOf returns the address portion of an addr/prefix string.
func hostOf(addrCIDR string) string {
	if ip, _, err := net.ParseCIDR(addrCIDR); err == nil {
		return ip.String()
	}
	return addrCIDR
}
