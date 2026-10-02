// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

// Phase is the high-level lifecycle state of a resource.
//
//	Pending -> Reconciling -> Ready
//	                       -> Error
//	           Deleting    -> Terminated
type Phase string

const (
	// PhasePending means the resource has been accepted but not yet acted on.
	PhasePending Phase = "Pending"
	// PhaseReconciling means a controller is actively driving the resource
	// toward its desired state.
	PhaseReconciling Phase = "Reconciling"
	// PhaseReady means the observed state matches the desired state.
	PhaseReady Phase = "Ready"
	// PhaseError means reconciliation failed and needs attention.
	PhaseError Phase = "Error"
	// PhaseDeleting means deletion was requested and finalizers are running.
	PhaseDeleting Phase = "Deleting"
	// PhaseTerminated means the resource has been fully cleaned up.
	PhaseTerminated Phase = "Terminated"
)
