// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// NodeRegistry is the typed store of nodes.
type NodeRegistry = registry.Registry[resource.NodeSpec, resource.NodeStatus]

// NodeHeartbeat is a reconcile pass that stamps the agent's own node with the
// current time, so the scheduler can tell live nodes from stale ones. It is a
// no-op when no node identity is configured.
type NodeHeartbeat struct {
	reg      *NodeRegistry
	nodeName string
	now      func() time.Time
}

// NewNodeHeartbeat returns a heartbeat pass for nodeName; an empty nodeName
// disables it.
func NewNodeHeartbeat(reg *NodeRegistry, nodeName string) *NodeHeartbeat {
	return &NodeHeartbeat{reg: reg, nodeName: nodeName, now: time.Now}
}

// Name identifies the reconcile pass.
func (h *NodeHeartbeat) Name() string { return "node-heartbeat" }

// ReconcileAll records the heartbeat timestamp on the agent's node.
func (h *NodeHeartbeat) ReconcileAll(_ context.Context) error {
	if h.nodeName == "" {
		return nil
	}
	node, err := h.reg.Get(h.nodeName)
	if errors.Is(err, state.ErrNotFound) {
		// The node is not registered with the control plane yet; nothing to stamp.
		return nil
	}
	if err != nil {
		return fmt.Errorf("manager: load node %q: %w", h.nodeName, err)
	}
	node.Status.LastSeen = h.now().UTC().Format(time.RFC3339)
	if err := h.reg.Put(node); err != nil {
		return fmt.Errorf("manager: heartbeat node %q: %w", h.nodeName, err)
	}
	return nil
}
