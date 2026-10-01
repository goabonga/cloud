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

// DiskSchedulerController places unscheduled disks onto nodes by least load
// and evicts a disk's placement once its node stops heartbeating, so it can
// be rescheduled. It is the disk counterpart of SchedulerController and
// MicroVMSchedulerController, duplicated for the same reason those two are:
// unrelated spec/status types.
//
// Unlike those two, it does not write node or node-pool status: a disk has
// no CPU/memory/pod requirement to account for, and node/pool readiness is
// already recomputed every pass by whichever of the other controllers runs
// in the same cycle. Placement (status.nodeName) is this controller's own
// responsibility and nothing else is.
type DiskSchedulerController struct {
	disks       *registry.Registry[resource.DiskSpec, resource.DiskStatus]
	nodes       *registry.Registry[resource.NodeSpec, resource.NodeStatus]
	readyWindow time.Duration
	now         func() time.Time
	logger      *slog.Logger
}

// NewDiskSchedulerController returns a scheduler reading from the disk and
// node stores. A node is schedulable only while its heartbeat is within
// readyWindow.
func NewDiskSchedulerController(
	disks *registry.Registry[resource.DiskSpec, resource.DiskStatus],
	nodes *registry.Registry[resource.NodeSpec, resource.NodeStatus],
	readyWindow time.Duration,
	logger *slog.Logger,
) *DiskSchedulerController {
	if logger == nil {
		logger = slog.Default()
	}
	return &DiskSchedulerController{disks: disks, nodes: nodes, readyWindow: readyWindow, now: time.Now, logger: logger}
}

// nodeReady reports whether a node has heartbeated within the ready window.
func (c *DiskSchedulerController) nodeReady(n *resource.Node, now time.Time) bool {
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
func (c *DiskSchedulerController) Name() string { return "disk-scheduler" }

// Reconcile assigns unscheduled disks to nodes and evicts the placement of a
// disk whose node is no longer ready (stale or deleted), so it can be
// rescheduled onto a live node. It is idempotent: placement is only ever
// changed, never torn down here.
func (c *DiskSchedulerController) Reconcile(ctx context.Context) error {
	nodes, err := c.nodes.List()
	if err != nil {
		return fmt.Errorf("controllers: list nodes: %w", err)
	}
	disks, err := c.disks.List()
	if err != nil {
		return fmt.Errorf("controllers: list disks: %w", err)
	}

	now := c.now()
	ready := make(map[string]bool, len(nodes))
	alloc := make(map[string]*nodeAlloc, len(nodes))
	for i := range nodes {
		alloc[nodes[i].Metadata.UID] = &nodeAlloc{}
		ready[nodes[i].Metadata.UID] = c.nodeReady(&nodes[i], now)
	}

	var errs []error
	// Evict a disk placed on a node that is no longer ready (stale or
	// deleted), clearing its placement so it can be rescheduled onto a live
	// node. This does not move the backing file itself: a disk evicted here
	// and rescheduled elsewhere starts out empty on its new node until a
	// replica (see the replication series this fix is a prerequisite for) is
	// promoted onto it.
	for i := range disks {
		d := &disks[i]
		if d.Metadata.IsDeleting() || d.Status.NodeName == "" || ready[d.Status.NodeName] {
			continue
		}
		if err := c.evict(d.Metadata.UID); err != nil {
			errs = append(errs, fmt.Errorf("evict %s: %w", d.Metadata.UID, err))
			continue
		}
		c.logger.InfoContext(ctx, "evicted disk", "disk", d.Metadata.UID, "node", d.Status.NodeName)
		d.Status.NodeName = ""
	}

	for i := range disks {
		d := &disks[i]
		if d.Metadata.IsDeleting() {
			continue
		}
		if a, ok := alloc[d.Status.NodeName]; ok {
			a.pods++
		}
	}

	for i := range disks {
		d := &disks[i]
		if d.Metadata.IsDeleting() || d.Status.NodeName != "" {
			continue
		}
		// A disk has no node-pool selector or CPU/memory requirement today,
		// so any ready node is eligible; pickNode still picks the
		// least-loaded one.
		node := pickNode(nodes, alloc, ready, nil, 0, 0)
		if node == "" {
			continue // no ready node this pass; retry later
		}
		if err := c.assign(d.Metadata.UID, node); err != nil {
			errs = append(errs, fmt.Errorf("schedule %s: %w", d.Metadata.UID, err))
			continue
		}
		alloc[node].pods++
		c.logger.InfoContext(ctx, "scheduled disk", "disk", d.Metadata.UID, "node", node)
	}

	return errors.Join(errs...)
}

// assign records the chosen node on the latest copy of the disk, preserving
// the fields the agent owns.
func (c *DiskSchedulerController) assign(uid, node string) error {
	cur, err := c.disks.Get(uid)
	if err != nil {
		return err
	}
	if cur.Status.NodeName == node {
		return nil
	}
	cur.Status.NodeName = node
	return c.disks.Put(cur)
}

// evict clears a disk's placement on the latest copy so it can be
// rescheduled, preserving the fields the agent owns.
func (c *DiskSchedulerController) evict(uid string) error {
	cur, err := c.disks.Get(uid)
	if err != nil {
		return err
	}
	if cur.Status.NodeName == "" {
		return nil
	}
	cur.Status.NodeName = ""
	return c.disks.Put(cur)
}
