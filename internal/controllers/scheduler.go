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

// nodeAlloc tracks the resources committed to a node.
type nodeAlloc struct {
	cpus float64
	mem  int
	pods int
}

// SchedulerController places unscheduled compute instances onto nodes by free
// capacity, honouring a compute's node-pool label selector, and records node
// allocation and node-pool readiness. It is a cluster-level controller and runs
// under leader election.
type SchedulerController struct {
	computes    *registry.Registry[resource.ComputeSpec, resource.ComputeStatus]
	nodes       *registry.Registry[resource.NodeSpec, resource.NodeStatus]
	pools       *registry.Registry[resource.NodePoolSpec, resource.NodePoolStatus]
	readyWindow time.Duration
	now         func() time.Time
	logger      *slog.Logger
}

// NewSchedulerController returns a scheduler reading from the compute, node and
// node-pool stores. A node is schedulable only while its heartbeat is within
// readyWindow.
func NewSchedulerController(
	computes *registry.Registry[resource.ComputeSpec, resource.ComputeStatus],
	nodes *registry.Registry[resource.NodeSpec, resource.NodeStatus],
	pools *registry.Registry[resource.NodePoolSpec, resource.NodePoolStatus],
	readyWindow time.Duration,
	logger *slog.Logger,
) *SchedulerController {
	if logger == nil {
		logger = slog.Default()
	}
	return &SchedulerController{computes: computes, nodes: nodes, pools: pools, readyWindow: readyWindow, now: time.Now, logger: logger}
}

// nodeReady reports whether a node has heartbeated within the ready window.
func (c *SchedulerController) nodeReady(n *resource.Node, now time.Time) bool {
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
func (c *SchedulerController) Name() string { return "scheduler" }

// Reconcile assigns unscheduled compute to nodes and updates node and node-pool
// status. It is idempotent: allocation is recomputed from the assigned compute
// each pass.
func (c *SchedulerController) Reconcile(ctx context.Context) error {
	bound := *c
	bound.computes = c.computes.WithContext(ctx)
	bound.nodes = c.nodes.WithContext(ctx)
	bound.pools = c.pools.WithContext(ctx)
	c = &bound
	nodes, err := c.nodes.List()
	if err != nil {
		return fmt.Errorf("controllers: list nodes: %w", err)
	}
	computes, err := c.computes.List()
	if err != nil {
		return fmt.Errorf("controllers: list computes: %w", err)
	}
	for i := range computes {
		computes[i].Spec = computes[i].Spec.WithDefaults()
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
	// Evict compute placed on a node that is no longer ready (stale or deleted),
	// clearing its placement so it can be rescheduled onto a live node.
	for i := range computes {
		cp := &computes[i]
		if cp.Metadata.IsDeleting() || cp.Status.NodeName == "" || ready[cp.Status.NodeName] {
			continue
		}
		if err := c.evict(cp.Metadata.UID); err != nil {
			errs = append(errs, fmt.Errorf("evict %s: %w", cp.Metadata.UID, err))
			continue
		}
		c.logger.InfoContext(ctx, "evicted compute", "compute", cp.Metadata.UID, "node", cp.Status.NodeName)
		cp.Status.NodeName = ""
	}

	for i := range computes {
		cp := &computes[i]
		if cp.Metadata.IsDeleting() {
			continue
		}
		if a, ok := alloc[cp.Status.NodeName]; ok {
			a.cpus += cp.Spec.CPU
			a.mem += cp.Spec.MemoryMB
			a.pods++
		}
	}

	for i := range computes {
		cp := &computes[i]
		if cp.Metadata.IsDeleting() || cp.Status.NodeName != "" {
			continue
		}
		selector, ok := c.poolSelector(pools, cp.Spec.NodePoolID)
		if !ok {
			continue // references a missing pool; cannot place
		}
		node := pickNode(nodes, alloc, ready, selector, cp.Spec.CPU, cp.Spec.MemoryMB)
		if node == "" {
			continue // no node fits this pass; retry later
		}
		if err := c.assign(cp.Metadata.UID, node); err != nil {
			errs = append(errs, fmt.Errorf("schedule %s: %w", cp.Metadata.UID, err))
			continue
		}
		a := alloc[node]
		a.cpus += cp.Spec.CPU
		a.mem += cp.Spec.MemoryMB
		a.pods++
		c.logger.InfoContext(ctx, "scheduled compute", "compute", cp.Metadata.UID, "node", node)
	}

	if err := c.writeNodeStatus(nodes, alloc, ready); err != nil {
		errs = append(errs, err)
	}
	if err := c.writePoolStatus(pools, nodes, ready); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// assign records the chosen node on the latest copy of the compute, preserving
// the fields the agent owns.
func (c *SchedulerController) assign(uid, node string) error {
	cur, err := c.computes.Get(uid)
	if err != nil {
		return err
	}
	if cur.Status.NodeName == node {
		return nil
	}
	cur.Status.NodeName = node
	return c.computes.Put(cur)
}

// evict clears a compute's placement on the latest copy so it can be
// rescheduled, preserving the fields the agent owns.
func (c *SchedulerController) evict(uid string) error {
	cur, err := c.computes.Get(uid)
	if err != nil {
		return err
	}
	if cur.Status.NodeName == "" {
		return nil
	}
	cur.Status.NodeName = ""
	return c.computes.Put(cur)
}

// poolSelector returns the label selector for a compute's node pool. When the
// pool id is empty the selector is nil (schedule anywhere); when it names a
// missing pool, ok is false.
func (c *SchedulerController) poolSelector(pools []resource.NodePool, poolID string) (map[string]string, bool) {
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

// writeNodeStatus records each node's recomputed allocation and its readiness
// derived from the heartbeat.
func (c *SchedulerController) writeNodeStatus(nodes []resource.Node, alloc map[string]*nodeAlloc, ready map[string]bool) error {
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
func (c *SchedulerController) writePoolStatus(pools []resource.NodePool, nodes []resource.Node, ready map[string]bool) error {
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

// pickNode returns the UID of the least-loaded ready node (by pod count) that
// matches the selector and has room for the request, or "" when none fits.
func pickNode(nodes []resource.Node, alloc map[string]*nodeAlloc, ready map[string]bool, selector map[string]string, cpu float64, mem int) string {
	best := ""
	bestPods := -1
	for i := range nodes {
		n := &nodes[i]
		if !ready[n.Metadata.UID] || !matchLabels(n.Spec.Labels, selector) {
			continue
		}
		a := alloc[n.Metadata.UID]
		if n.Spec.Capacity.MaxPods > 0 && a.pods >= n.Spec.Capacity.MaxPods {
			continue
		}
		if a.cpus+cpu > float64(n.Spec.Capacity.CPUs) {
			continue
		}
		if a.mem+mem > n.Spec.Capacity.MemoryMB {
			continue
		}
		if best == "" || a.pods < bestPods {
			best = n.Metadata.UID
			bestPods = a.pods
		}
	}
	return best
}

// matchLabels reports whether labels satisfy every key/value in selector. An
// empty selector matches anything.
func matchLabels(labels, selector map[string]string) bool {
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}
