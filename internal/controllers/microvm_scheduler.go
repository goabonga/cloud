// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package controllers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
)

// MicroVMSchedulerController places unscheduled micro-VMs onto nodes by free
// capacity, honouring a micro-VM's node-pool label selector, and records node
// allocation and node-pool readiness. It is the micro-VM counterpart of
// SchedulerController, duplicated rather than shared because the two
// resources have unrelated spec/status types; see pickNode and matchLabels
// for the logic they do share.
//
// Known limitation: this controller and SchedulerController each recompute a
// node's status.allocated from only their own kind's instances, so whichever
// one reconciles last overwrites the other's contribution in that reported
// field, and pickNode's own capacity check is similarly blind to the other
// kind's load. A node pool that schedules both compute and microvm can be
// oversubscribed as a result; keep them in disjoint node pools (nodePoolId)
// until capacity accounting is made aware of both kinds together.
type MicroVMSchedulerController struct {
	microvms    *registry.Registry[resource.MicroVMSpec, resource.MicroVMStatus]
	nodes       *registry.Registry[resource.NodeSpec, resource.NodeStatus]
	pools       *registry.Registry[resource.NodePoolSpec, resource.NodePoolStatus]
	readyWindow time.Duration
	now         func() time.Time
	logger      *slog.Logger
}

// NewMicroVMSchedulerController returns a scheduler reading from the
// micro-VM, node and node-pool stores. A node is schedulable only while its
// heartbeat is within readyWindow.
func NewMicroVMSchedulerController(
	microvms *registry.Registry[resource.MicroVMSpec, resource.MicroVMStatus],
	nodes *registry.Registry[resource.NodeSpec, resource.NodeStatus],
	pools *registry.Registry[resource.NodePoolSpec, resource.NodePoolStatus],
	readyWindow time.Duration,
	logger *slog.Logger,
) *MicroVMSchedulerController {
	if logger == nil {
		logger = slog.Default()
	}
	return &MicroVMSchedulerController{microvms: microvms, nodes: nodes, pools: pools, readyWindow: readyWindow, now: time.Now, logger: logger}
}

// nodeReady reports whether a node has heartbeated within the ready window.
func (c *MicroVMSchedulerController) nodeReady(n *resource.Node, now time.Time) bool {
	if n.Status.LastSeen == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, n.Status.LastSeen)
	if err != nil {
		return false
	}
	return now.Sub(t) <= c.readyWindow
}

// Name identifies the controller.
func (c *MicroVMSchedulerController) Name() string { return "microvm-scheduler" }

// Reconcile assigns unscheduled micro-VMs to nodes and updates node and
// node-pool status. It is idempotent: allocation is recomputed from the
// assigned micro-VMs each pass.
func (c *MicroVMSchedulerController) Reconcile(ctx context.Context) error {
	bound := *c
	bound.microvms = c.microvms.WithContext(ctx)
	bound.nodes = c.nodes.WithContext(ctx)
	bound.pools = c.pools.WithContext(ctx)
	c = &bound
	nodes, err := c.nodes.List()
	if err != nil {
		return fmt.Errorf("controllers: list nodes: %w", err)
	}
	microvms, err := c.microvms.List()
	if err != nil {
		return fmt.Errorf("controllers: list microvms: %w", err)
	}
	pools, err := c.pools.List()
	if err != nil {
		return fmt.Errorf("controllers: list node pools: %w", err)
	}

	now := c.now()
	ready := make(map[string]bool, len(nodes))
	alloc := make(map[string]*nodeAlloc, len(nodes))
	for i := range nodes {
		alloc[nodes[i].Metadata.UID] = &nodeAlloc{}
		ready[nodes[i].Metadata.UID] = c.nodeReady(&nodes[i], now)
	}

	var errs []error
	// Evict a micro-VM placed on a node that is no longer ready (stale or
	// deleted), clearing its placement so it can be rescheduled onto a live
	// node.
	for i := range microvms {
		v := &microvms[i]
		if v.Metadata.IsDeleting() || v.Status.NodeName == "" || ready[v.Status.NodeName] {
			continue
		}
		if err := c.evict(v.Metadata.UID); err != nil {
			errs = append(errs, fmt.Errorf("evict %s: %w", v.Metadata.UID, err))
			continue
		}
		c.logger.InfoContext(ctx, "evicted microvm", "microvm", v.Metadata.UID, "node", v.Status.NodeName)
		v.Status.NodeName = ""
	}

	for i := range microvms {
		v := &microvms[i]
		if v.Metadata.IsDeleting() {
			continue
		}
		if a, ok := alloc[v.Status.NodeName]; ok {
			a.cpus += float64(v.Spec.VCPUs)
			a.mem += v.Spec.MemoryMB
			a.pods++
		}
	}

	for i := range microvms {
		v := &microvms[i]
		if v.Metadata.IsDeleting() || v.Status.NodeName != "" {
			continue
		}
		selector, ok := c.poolSelector(pools, v.Spec.NodePoolID)
		if !ok {
			continue // references a missing pool; cannot place
		}
		node := pickNode(nodes, alloc, ready, selector, float64(v.Spec.VCPUs), v.Spec.MemoryMB)
		if node == "" {
			continue // no node fits this pass; retry later
		}
		if err := c.assign(v.Metadata.UID, node); err != nil {
			errs = append(errs, fmt.Errorf("schedule %s: %w", v.Metadata.UID, err))
			continue
		}
		a := alloc[node]
		a.cpus += float64(v.Spec.VCPUs)
		a.mem += v.Spec.MemoryMB
		a.pods++
		c.logger.InfoContext(ctx, "scheduled microvm", "microvm", v.Metadata.UID, "node", node)
	}

	if err := c.writeNodeStatus(nodes, alloc, ready); err != nil {
		errs = append(errs, err)
	}
	if err := c.writePoolStatus(pools, nodes, ready); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// assign records the chosen node on the latest copy of the micro-VM,
// preserving the fields the agent owns.
func (c *MicroVMSchedulerController) assign(uid, node string) error {
	cur, err := c.microvms.Get(uid)
	if err != nil {
		return err
	}
	if cur.Status.NodeName == node {
		return nil
	}
	cur.Status.NodeName = node
	return c.microvms.Put(cur)
}

// evict clears a micro-VM's placement on the latest copy so it can be
// rescheduled, preserving the fields the agent owns.
func (c *MicroVMSchedulerController) evict(uid string) error {
	cur, err := c.microvms.Get(uid)
	if err != nil {
		return err
	}
	if cur.Status.NodeName == "" {
		return nil
	}
	cur.Status.NodeName = ""
	return c.microvms.Put(cur)
}

// poolSelector returns the label selector for a micro-VM's node pool. When
// the pool id is empty the selector is nil (schedule anywhere); when it
// names a missing pool, ok is false.
func (c *MicroVMSchedulerController) poolSelector(pools []resource.NodePool, poolID string) (map[string]string, bool) {
	if poolID == "" {
		return nil, true
	}
	for i := range pools {
		if pools[i].Metadata.UID == poolID {
			return pools[i].Spec.NodeSelector, true
		}
	}
	return nil, false
}

// writeNodeStatus records each node's recomputed allocation and its
// readiness derived from the heartbeat.
func (c *MicroVMSchedulerController) writeNodeStatus(nodes []resource.Node, alloc map[string]*nodeAlloc, ready map[string]bool) error {
	var errs []error
	for i := range nodes {
		n := &nodes[i]
		a := alloc[n.Metadata.UID]
		want := resource.NodeAllocated{CPUs: a.cpus, MemoryMB: a.mem, Pods: a.pods}
		phase, reason, msg := resource.PhaseReady, "Ready", "node available for scheduling"
		if !ready[n.Metadata.UID] {
			phase = resource.PhasePending
			if n.Status.LastSeen == "" {
				reason, msg = "NeverSeen", "node has not heartbeated"
			} else {
				reason, msg = "Stale", "node heartbeat is stale"
			}
		}
		if n.Status.Allocated == want && n.Status.Phase == phase {
			continue
		}
		n.Status.Allocated = want
		n.Status.SetPhase(phase, reason, msg)
		if phase == resource.PhaseReady {
			n.Status.MarkReconciled(n.Metadata.Generation)
		}
		if err := c.nodes.Put(n); err != nil {
			errs = append(errs, fmt.Errorf("controllers: save node %s: %w", n.Metadata.UID, err))
		}
	}
	return errors.Join(errs...)
}

// writePoolStatus records each pool's member and ready node counts.
func (c *MicroVMSchedulerController) writePoolStatus(pools []resource.NodePool, nodes []resource.Node, ready map[string]bool) error {
	var errs []error
	for i := range pools {
		p := &pools[i]
		total, readyCount := 0, 0
		for j := range nodes {
			if !matchLabels(nodes[j].Spec.Labels, p.Spec.NodeSelector) {
				continue
			}
			total++
			if ready[nodes[j].Metadata.UID] {
				readyCount++
			}
		}
		if p.Status.TotalNodes == total && p.Status.ReadyNodes == readyCount && p.Status.Phase == resource.PhaseReady {
			continue
		}
		p.Status.TotalNodes = total
		p.Status.ReadyNodes = readyCount
		p.Status.SetPhase(resource.PhaseReady, "Counted", "node pool reconciled")
		p.Status.MarkReconciled(p.Metadata.Generation)
		if err := c.pools.Put(p); err != nil {
			errs = append(errs, fmt.Errorf("controllers: save node pool %s: %w", p.Metadata.UID, err))
		}
	}
	return errors.Join(errs...)
}
