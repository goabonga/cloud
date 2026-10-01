// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "fmt"

// KindFunctionInstance is the resource kind for function instances: the join
// between a Function and the Compute realizing one of its warm-pool slots.
const KindFunctionInstance = "function_instance"

// FunctionInstanceFinalizer is attached by the function controller so the
// backing compute finishes tearing down before the instance record is
// removed.
const FunctionInstanceFinalizer = "infra.io/function-instance"

// FunctionInstanceWarm is the State of an instance sitting idle in the pool,
// ready to serve an invocation. More states (e.g. "Assigned") are added once
// the invoke path exists and can claim an instance out of the pool.
const FunctionInstanceWarm = "Warm"

// FunctionInstanceSpec is the desired state of one function-instance pool
// slot: which function it backs and which compute realizes it. It is created
// and owned by the function controller, never directly by a client.
type FunctionInstanceSpec struct {
	FunctionID string `json:"functionId"`
	ComputeID  string `json:"computeId"`
}

// Validate reports whether the spec is well-formed.
func (s FunctionInstanceSpec) Validate() error {
	if s.FunctionID == "" {
		return fmt.Errorf("function_instance: functionId is required")
	}
	if s.ComputeID == "" {
		return fmt.Errorf("function_instance: computeId is required")
	}
	return nil
}

// FunctionInstanceStatus is the observed state of a function instance. Phase
// tracks the backing compute's own phase (Pending/Reconciling/Ready/Error);
// it is not meaningful on its own, only alongside State.
type FunctionInstanceStatus struct {
	StatusBase
	// State is "Warm" while the instance sits idle in the pool.
	State string `json:"state,omitempty"`
	// LastUsedAt is the RFC3339 time the instance was last released back to
	// the pool; it drives idle-TTL eviction.
	LastUsedAt string `json:"lastUsedAt,omitempty"`
}

// FunctionInstance is a function-instance resource.
type FunctionInstance = Resource[FunctionInstanceSpec, FunctionInstanceStatus]
