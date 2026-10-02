// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource_test

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestSetConditionAppendAndUpdate(t *testing.T) {
	t.Parallel()

	var s resource.StatusBase
	s.SetCondition(resource.ReadyCondition("Created", "vpc created"))
	if got := len(s.Conditions); got != 1 {
		t.Fatalf("expected 1 condition, got %d", got)
	}

	// Same type updates in place rather than appending.
	s.SetCondition(resource.NotReadyCondition("Drift", "spec changed"))
	if got := len(s.Conditions); got != 1 {
		t.Fatalf("expected condition to be replaced, got %d", got)
	}
	c := s.GetCondition(resource.ConditionReady)
	if c == nil {
		t.Fatal("expected Ready condition")
	}
	if c.Status != resource.ConditionFalse || c.Reason != "Drift" {
		t.Fatalf("unexpected condition: %+v", c)
	}

	// A different type appends.
	s.SetCondition(resource.SyncedCondition("Applied", "ok"))
	if got := len(s.Conditions); got != 2 {
		t.Fatalf("expected 2 conditions, got %d", got)
	}
}

func TestGetConditionMissing(t *testing.T) {
	t.Parallel()

	var s resource.StatusBase
	if c := s.GetCondition(resource.ConditionHealthy); c != nil {
		t.Fatalf("expected nil for missing condition, got %+v", c)
	}
}

func TestGenerationHelpers(t *testing.T) {
	t.Parallel()

	var s resource.StatusBase
	if !s.NeedsReconcile(1) {
		t.Fatal("generation 1 vs observed 0 should need reconcile")
	}

	s.MarkReconciled(1)
	if s.NeedsReconcile(1) {
		t.Fatal("after MarkReconciled(1), generation 1 should not need reconcile")
	}
	if s.IsConverged(1) {
		t.Fatal("not converged until phase is Ready")
	}

	s.SetPhase(resource.PhaseReady, "Reconciled", "ok")
	if !s.IsConverged(1) {
		t.Fatal("reconciled + Ready should be converged")
	}
	if s.IsConverged(2) {
		t.Fatal("generation 2 is not yet observed")
	}
}

func TestSetPhaseConditions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		phase      resource.Phase
		wantReady  resource.ConditionStatus
		hasReady   bool
		isReadyAcc bool
	}{
		{"ready", resource.PhaseReady, resource.ConditionTrue, true, true},
		{"reconciling", resource.PhaseReconciling, "", false, false},
		{"error", resource.PhaseError, resource.ConditionFalse, true, false},
		{"deleting", resource.PhaseDeleting, resource.ConditionFalse, true, false},
		{"pending", resource.PhasePending, resource.ConditionFalse, true, false},
		{"terminated", resource.PhaseTerminated, "", false, false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var s resource.StatusBase
			s.SetPhase(tc.phase, "Reason", "message")

			if s.Phase != tc.phase {
				t.Fatalf("phase = %q, want %q", s.Phase, tc.phase)
			}
			if s.IsReady() != tc.isReadyAcc {
				t.Fatalf("IsReady() = %v, want %v", s.IsReady(), tc.isReadyAcc)
			}

			ready := s.GetCondition(resource.ConditionReady)
			if tc.hasReady {
				if ready == nil {
					t.Fatal("expected a Ready condition")
				}
				if ready.Status != tc.wantReady {
					t.Fatalf("Ready status = %q, want %q", ready.Status, tc.wantReady)
				}
			} else if ready != nil {
				t.Fatalf("did not expect a Ready condition, got %+v", ready)
			}
		})
	}
}
