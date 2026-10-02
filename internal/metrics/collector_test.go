// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package metrics_test

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/metrics"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func TestCollectorCountsByKindAndPhase(t *testing.T) {
	t.Parallel()

	store := state.NewFileStore(t.TempDir())
	reg := registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC)

	ready := &resource.VPC{Metadata: resource.ObjectMeta{UID: "vpc-1"}, Spec: resource.VPCSpec{CIDR: "10.0.0.0/16"}}
	ready.Status.SetPhase(resource.PhaseReady, "Reconciled", "ok")
	pending := &resource.VPC{Metadata: resource.ObjectMeta{UID: "vpc-2"}, Spec: resource.VPCSpec{CIDR: "10.1.0.0/16"}}
	for _, v := range []*resource.VPC{ready, pending} {
		if err := reg.Put(v); err != nil {
			t.Fatalf("seed %s: %v", v.Metadata.UID, err)
		}
	}

	c := metrics.NewCollector(store, resource.KindVPC, resource.KindACLPolicy)

	expected := `
# HELP infra_resources_total Number of resources of a kind in the store.
# TYPE infra_resources_total gauge
infra_resources_total{kind="acl_policy"} 0
infra_resources_total{kind="vpc"} 2
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected), "infra_resources_total"); err != nil {
		t.Fatalf("total mismatch: %v", err)
	}

	byPhase := `
# HELP infra_resources_by_phase Number of resources of a kind grouped by status phase.
# TYPE infra_resources_by_phase gauge
infra_resources_by_phase{kind="vpc",phase="Ready"} 1
infra_resources_by_phase{kind="vpc",phase="Unknown"} 1
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(byPhase), "infra_resources_by_phase"); err != nil {
		t.Fatalf("by-phase mismatch: %v", err)
	}
}

func TestCollectorRegisters(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	if err := reg.Register(metrics.NewCollector(state.NewFileStore(t.TempDir()), resource.KindVPC)); err != nil {
		t.Fatalf("register: %v", err)
	}
}
