// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func newNodeRegistry(t *testing.T) *manager.NodeRegistry {
	t.Helper()
	return registry.New[resource.NodeSpec, resource.NodeStatus](state.NewFileStore(t.TempDir()), resource.KindNode)
}

func TestNodeHeartbeatStampsLastSeen(t *testing.T) {
	t.Parallel()

	reg := newNodeRegistry(t)
	if err := reg.Put(&resource.Node{
		Metadata: resource.ObjectMeta{UID: "node-1", Generation: 1},
		Spec:     resource.NodeSpec{Hostname: "h1", Address: "10.0.0.2", Capacity: resource.NodeCapacity{CPUs: 4, MemoryMB: 8192}},
	}); err != nil {
		t.Fatalf("seed node: %v", err)
	}

	hb := manager.NewNodeHeartbeat(reg, "node-1")
	if hb.Name() != "node-heartbeat" {
		t.Fatalf("name = %q", hb.Name())
	}
	if err := hb.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	got, _ := reg.Get("node-1")
	if got.Status.LastSeen == "" {
		t.Fatal("lastSeen should be stamped")
	}
}

func TestNodeHeartbeatNoopWithoutIdentity(t *testing.T) {
	t.Parallel()

	reg := newNodeRegistry(t)
	hb := manager.NewNodeHeartbeat(reg, "")
	if err := hb.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
}

func TestNodeHeartbeatMissingNode(t *testing.T) {
	t.Parallel()

	reg := newNodeRegistry(t)
	hb := manager.NewNodeHeartbeat(reg, "node-absent")
	if err := hb.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("heartbeat for an unregistered node should be a no-op, got %v", err)
	}
}
