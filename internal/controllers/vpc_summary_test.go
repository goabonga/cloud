// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package controllers_test

import (
	"context"
	"testing"

	"github.com/goabonga/infrastructure/internal/controllers"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func TestVPCSummaryCountsByPhase(t *testing.T) {
	t.Parallel()

	reg := registry.New[resource.VPCSpec, resource.VPCStatus](state.NewFileStore(t.TempDir()), resource.KindVPC)
	seed := []struct {
		uid   string
		phase resource.Phase
	}{
		{"vpc-ready", resource.PhaseReady},
		{"vpc-ready2", resource.PhaseReady},
		{"vpc-pending", resource.PhasePending},
		{"vpc-error", resource.PhaseError},
		{"vpc-fresh", ""}, // never reconciled counts as pending
	}
	for _, s := range seed {
		v := &resource.VPC{Metadata: resource.ObjectMeta{UID: s.uid}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
		v.Status.Phase = s.phase
		if err := reg.Put(v); err != nil {
			t.Fatalf("seed %s: %v", s.uid, err)
		}
	}

	ctrl := controllers.NewVPCSummaryController(reg, nil)
	if err := ctrl.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got := ctrl.Summary()
	want := controllers.VPCSummary{Total: 5, Ready: 2, Pending: 2, Error: 1}
	if got != want {
		t.Fatalf("summary = %+v, want %+v", got, want)
	}
}

func TestVPCSummaryName(t *testing.T) {
	t.Parallel()

	ctrl := controllers.NewVPCSummaryController(nil, nil)
	if ctrl.Name() != "vpc-summary" {
		t.Fatalf("name = %q", ctrl.Name())
	}
}
