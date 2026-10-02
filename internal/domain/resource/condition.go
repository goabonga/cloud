// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "time"

// ConditionStatus is the tri-state value of a condition.
type ConditionStatus string

const (
	// ConditionTrue means the condition holds.
	ConditionTrue ConditionStatus = "True"
	// ConditionFalse means the condition does not hold.
	ConditionFalse ConditionStatus = "False"
	// ConditionUnknown means the condition could not be determined.
	ConditionUnknown ConditionStatus = "Unknown"
)

// Standard condition types. Several can be true at once (e.g. Healthy + Synced).
const (
	// ConditionReady indicates the resource is fully operational.
	ConditionReady = "Ready"
	// ConditionSynced indicates the spec has been reconciled to the runtime.
	ConditionSynced = "Synced"
	// ConditionHealthy indicates the resource passes health checks.
	ConditionHealthy = "Healthy"
	// ConditionProgressing indicates the resource is being reconciled.
	ConditionProgressing = "Progressing"
	// ConditionDegraded indicates the resource is only partially functional.
	ConditionDegraded = "Degraded"
	// ConditionScheduled indicates a placement decision has been made.
	ConditionScheduled = "Scheduled"
	// ConditionBound indicates a binding resource has been created.
	ConditionBound = "Bound"
)

// Condition describes one observable aspect of a resource's current state.
type Condition struct {
	// Type is the condition name (e.g. "Ready", "Synced", "Healthy").
	Type string `json:"type"`
	// Status is "True", "False" or "Unknown".
	Status ConditionStatus `json:"status"`
	// Reason is a one-word CamelCase identifier for the condition's cause.
	Reason string `json:"reason,omitempty"`
	// Message is a human-readable explanation.
	Message string `json:"message,omitempty"`
	// LastTransitionTime is the last time the condition changed.
	LastTransitionTime time.Time `json:"lastTransitionTime"`
}

// NewCondition builds a condition stamped with the current time.
func NewCondition(condType string, status ConditionStatus, reason, message string) Condition {
	return Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: time.Now(),
	}
}

// ReadyCondition builds a Ready=True condition.
func ReadyCondition(reason, message string) Condition {
	return NewCondition(ConditionReady, ConditionTrue, reason, message)
}

// NotReadyCondition builds a Ready=False condition.
func NotReadyCondition(reason, message string) Condition {
	return NewCondition(ConditionReady, ConditionFalse, reason, message)
}

// SyncedCondition builds a Synced=True condition.
func SyncedCondition(reason, message string) Condition {
	return NewCondition(ConditionSynced, ConditionTrue, reason, message)
}

// ProgressingCondition builds a Progressing=True condition.
func ProgressingCondition(reason, message string) Condition {
	return NewCondition(ConditionProgressing, ConditionTrue, reason, message)
}

// ErrorCondition builds a Ready=False condition with the "Error" reason.
func ErrorCondition(message string) Condition {
	return NewCondition(ConditionReady, ConditionFalse, "Error", message)
}

// StatusBase carries the fields common to every resource status. Concrete
// status types embed it so the controller framework can read phase and
// conditions generically.
type StatusBase struct {
	// ObservedGeneration is the last metadata.generation a controller acted on.
	// When it equals metadata.generation, the spec is fully reconciled.
	ObservedGeneration int64 `json:"observedGeneration"`
	// Phase is the high-level lifecycle state.
	Phase Phase `json:"phase"`
	// Conditions is the set of observable condition flags.
	Conditions []Condition `json:"conditions,omitempty"`
}

// GetBase returns the embedded StatusBase. Promoted to embedding status types so
// the generic framework can reach phase and conditions.
func (s *StatusBase) GetBase() *StatusBase {
	return s
}

// SetCondition inserts or replaces a condition by type.
func (s *StatusBase) SetCondition(c Condition) {
	for i := range s.Conditions {
		if s.Conditions[i].Type == c.Type {
			s.Conditions[i] = c
			return
		}
	}
	s.Conditions = append(s.Conditions, c)
}

// GetCondition returns the condition of the given type, or nil if absent.
func (s *StatusBase) GetCondition(condType string) *Condition {
	for i := range s.Conditions {
		if s.Conditions[i].Type == condType {
			return &s.Conditions[i]
		}
	}
	return nil
}

// IsReady reports whether the resource is in the Ready phase.
func (s *StatusBase) IsReady() bool {
	return s.Phase == PhaseReady
}

// NeedsReconcile reports whether the spec changed since the last reconcile.
func (s *StatusBase) NeedsReconcile(generation int64) bool {
	return s.ObservedGeneration < generation
}

// MarkReconciled records that the controller has processed the given generation.
func (s *StatusBase) MarkReconciled(generation int64) {
	s.ObservedGeneration = generation
}

// IsConverged reports whether the given generation is reconciled and Ready.
func (s *StatusBase) IsConverged(generation int64) bool {
	return s.ObservedGeneration >= generation && s.Phase == PhaseReady
}

// SetPhase sets the phase and updates the derived conditions accordingly.
func (s *StatusBase) SetPhase(phase Phase, reason, message string) {
	s.Phase = phase
	switch phase {
	case PhaseReady:
		s.SetCondition(ReadyCondition(reason, message))
		s.SetCondition(NewCondition(ConditionProgressing, ConditionFalse, reason, message))
	case PhaseReconciling:
		s.SetCondition(ProgressingCondition(reason, message))
	case PhaseError:
		s.SetCondition(ErrorCondition(message))
		s.SetCondition(NewCondition(ConditionProgressing, ConditionFalse, reason, message))
	case PhaseDeleting:
		s.SetCondition(NewCondition(ConditionReady, ConditionFalse, "Deleting", message))
		s.SetCondition(ProgressingCondition("Deleting", message))
	case PhasePending:
		s.SetCondition(NewCondition(ConditionReady, ConditionFalse, "Pending", message))
	case PhaseTerminated:
		// No condition update once terminated.
	}
}
