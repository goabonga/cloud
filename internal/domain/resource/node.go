// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "fmt"

// Node resource kinds.
const (
	KindNode     = "node"
	KindNodePool = "node_pool"
)

// NodeCapacity is the total schedulable capacity of a node.
type NodeCapacity struct {
	CPUs     int `json:"cpus"`
	MemoryMB int `json:"memoryMb"`
	MaxPods  int `json:"maxPods,omitempty"`
}

// NodeSpec is the desired state of a host registered with the control plane.
type NodeSpec struct {
	Hostname string            `json:"hostname"`
	Address  string            `json:"address"`
	Labels   map[string]string `json:"labels,omitempty"`
	Capacity NodeCapacity      `json:"capacity"`
}

// Validate reports whether the spec is well-formed.
func (s NodeSpec) Validate() error {
	if s.Hostname == "" {
		return fmt.Errorf("node: hostname is required")
	}
	if s.Address == "" {
		return fmt.Errorf("node: address is required")
	}
	if s.Capacity.CPUs <= 0 || s.Capacity.MemoryMB <= 0 {
		return fmt.Errorf("node: capacity cpus and memoryMb must be positive")
	}
	return nil
}

// NodeAllocated is the capacity currently committed to workloads on a node.
type NodeAllocated struct {
	CPUs     float64 `json:"cpus"`
	MemoryMB int     `json:"memoryMb"`
	Pods     int     `json:"pods"`
}

// NodeStatus is the observed state of a node.
type NodeStatus struct {
	StatusBase
	Allocated NodeAllocated `json:"allocated"`
	LastSeen  string        `json:"lastSeen,omitempty"`
}

// Node is a host resource.
type Node = Resource[NodeSpec, NodeStatus]

// NodePoolSpec is the desired state of a pool of nodes selected by label.
type NodePoolSpec struct {
	Name         string            `json:"name"`
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	MinNodes     int               `json:"minNodes,omitempty"`
	MaxNodes     int               `json:"maxNodes,omitempty"`
}

// Validate reports whether the spec is well-formed.
func (s NodePoolSpec) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("node_pool: name is required")
	}
	if s.MinNodes < 0 || s.MaxNodes < 0 {
		return fmt.Errorf("node_pool: minNodes and maxNodes must not be negative")
	}
	if s.MaxNodes > 0 && s.MaxNodes < s.MinNodes {
		return fmt.Errorf("node_pool: maxNodes must be >= minNodes")
	}
	return nil
}

// NodePoolStatus is the observed state of a node pool.
type NodePoolStatus struct {
	StatusBase
	ReadyNodes int `json:"readyNodes"`
	TotalNodes int `json:"totalNodes"`
}

// NodePool is a node-pool resource.
type NodePool = Resource[NodePoolSpec, NodePoolStatus]
