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

// AsyncDiskReplicaSchedulerController places unscheduled async disk
// replicas onto a ready node other than their disk's current primary node,
// honouring the replica's target node-pool selector when set. It is the
// disk-replica counterpart of DiskSchedulerController: same liveness rule
// and eviction, same no-capacity-accounting rationale (a replica has no
// CPU/memory/pod requirement), plus the one thing specific to a replica -
// it must never land where the disk it is replicating already is.
type AsyncDiskReplicaSchedulerController struct {
	replicas    *registry.Registry[resource.AsyncDiskReplicaSpec, resource.AsyncDiskReplicaStatus]
	disks       *registry.Registry[resource.DiskSpec, resource.DiskStatus]
	nodes       *registry.Registry[resource.NodeSpec, resource.NodeStatus]
	pools       *registry.Registry[resource.NodePoolSpec, resource.NodePoolStatus]
	readyWindow time.Duration
	now         func() time.Time
	logger      *slog.Logger
}

// NewAsyncDiskReplicaSchedulerController returns a scheduler reading from
// the async-disk-replica, disk, node and node-pool stores. A node is
// schedulable only while its heartbeat is within readyWindow.
func NewAsyncDiskReplicaSchedulerController(
	replicas *registry.Registry[resource.AsyncDiskReplicaSpec, resource.AsyncDiskReplicaStatus],
	disks *registry.Registry[resource.DiskSpec, resource.DiskStatus],
	nodes *registry.Registry[resource.NodeSpec, resource.NodeStatus],
	pools *registry.Registry[resource.NodePoolSpec, resource.NodePoolStatus],
	readyWindow time.Duration,
	logger *slog.Logger,
) *AsyncDiskReplicaSchedulerController {
	if logger == nil {
		logger = slog.Default()
	}
	return &AsyncDiskReplicaSchedulerController{
		replicas: replicas, disks: disks, nodes: nodes, pools: pools,
		readyWindow: readyWindow, now: time.Now, logger: logger,
	}
}

func (c *AsyncDiskReplicaSchedulerController) nodeReady(n *resource.Node, now time.Time) bool {
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
func (c *AsyncDiskReplicaSchedulerController) Name() string { return "async-disk-replica-scheduler" }

// Reconcile assigns unscheduled replicas to nodes and evicts the placement
// of a replica whose node is no longer ready. It is idempotent.
func (c *AsyncDiskReplicaSchedulerController) Reconcile(ctx context.Context) error {
	nodes, err := c.nodes.List()
	if err != nil {
		return fmt.Errorf("controllers: list nodes: %w", err)
	}
	replicas, err := c.replicas.List()
	if err != nil {
		return fmt.Errorf("controllers: list async disk replicas: %w", err)
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
	for i := range replicas {
		r := &replicas[i]
		if r.Metadata.IsDeleting() || r.Status.NodeName == "" || ready[r.Status.NodeName] {
			continue
		}
		if err := c.evict(r.Metadata.UID); err != nil {
			errs = append(errs, fmt.Errorf("evict %s: %w", r.Metadata.UID, err))
			continue
		}
		c.logger.InfoContext(ctx, "evicted async disk replica", "replica", r.Metadata.UID, "node", r.Status.NodeName)
		r.Status.NodeName = ""
	}

	for i := range replicas {
		r := &replicas[i]
		if r.Metadata.IsDeleting() {
			continue
		}
		if a, ok := alloc[r.Status.NodeName]; ok {
			a.pods++
		}
	}

	for i := range replicas {
		r := &replicas[i]
		if r.Metadata.IsDeleting() || r.Status.NodeName != "" {
			continue
		}
		disk, err := c.disks.Get(r.Spec.DiskID)
		if err != nil || disk.Status.NodeName == "" {
			continue // the disk, or its placement, isn't there yet; retry later
		}
		selector, ok := c.poolSelector(pools, r.Spec.TargetNodePoolID)
		if !ok {
			continue // references a missing pool; cannot place
		}
		node := pickNodeExcluding(nodes, alloc, ready, selector, disk.Status.NodeName)
		if node == "" {
			continue // no node fits this pass; retry later
		}
		if err := c.assign(r.Metadata.UID, node); err != nil {
			errs = append(errs, fmt.Errorf("schedule %s: %w", r.Metadata.UID, err))
			continue
		}
		alloc[node].pods++
		c.logger.InfoContext(ctx, "scheduled async disk replica", "replica", r.Metadata.UID, "node", node, "disk", r.Spec.DiskID)
	}

	return errors.Join(errs...)
}

func (c *AsyncDiskReplicaSchedulerController) assign(uid, node string) error {
	cur, err := c.replicas.Get(uid)
	if err != nil {
		return err
	}
	if cur.Status.NodeName == node {
		return nil
	}
	cur.Status.NodeName = node
	return c.replicas.Put(cur)
}

func (c *AsyncDiskReplicaSchedulerController) evict(uid string) error {
	cur, err := c.replicas.Get(uid)
	if err != nil {
		return err
	}
	if cur.Status.NodeName == "" {
		return nil
	}
	cur.Status.NodeName = ""
	return c.replicas.Put(cur)
}

// poolSelector returns the label selector for a replica's target node pool.
// When the pool id is empty the selector is nil (schedule anywhere); when
// it names a missing pool, ok is false.
func (c *AsyncDiskReplicaSchedulerController) poolSelector(pools []resource.NodePool, poolID string) (map[string]string, bool) {
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

// pickNodeExcluding returns the UID of the least-loaded ready node (by pod
// count) that matches the selector and isn't exclude, or "" when none fits.
// Unlike pickNode it has no CPU/memory request to check: a replica has
// none.
func pickNodeExcluding(nodes []resource.Node, alloc map[string]*nodeAlloc, ready map[string]bool, selector map[string]string, exclude string) string {
	best := ""
	bestPods := -1
	for i := range nodes {
		n := &nodes[i]
		if n.Metadata.UID == exclude || !ready[n.Metadata.UID] || !matchLabels(n.Spec.Labels, selector) {
			continue
		}
		a := alloc[n.Metadata.UID]
		if best == "" || a.pods < bestPods {
			best = n.Metadata.UID
			bestPods = a.pods
		}
	}
	return best
}
