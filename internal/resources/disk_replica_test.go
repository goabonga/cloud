// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestAsyncDiskReplicaResourceMetadata(t *testing.T) {
	t.Parallel()

	var resp resource.MetadataResponse
	NewAsyncDiskReplicaResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "infra"}, &resp)
	if resp.TypeName != "infra_async_disk_replica" {
		t.Fatalf("TypeName = %q, want infra_async_disk_replica", resp.TypeName)
	}
}

func TestAsyncDiskReplicaResourceSchema(t *testing.T) {
	t.Parallel()

	var resp resource.SchemaResponse
	NewAsyncDiskReplicaResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	attrs := resp.Schema.Attributes

	if !attrs["disk_id"].IsRequired() {
		t.Fatal("disk_id should be required")
	}
	for _, computed := range []string{"id", "node_name", "last_synced_at", "bytes_synced", "phase"} {
		if !attrs[computed].IsComputed() {
			t.Fatalf("%s should be computed", computed)
		}
	}
}

func TestAsyncDiskReplicaToSpecAndBack(t *testing.T) {
	t.Parallel()

	model := asyncDiskReplicaModel{
		ID:               types.StringValue("replica-1"),
		DiskID:           types.StringValue("disk-1"),
		TargetNodePoolID: types.StringValue("pool-b"),
		IntervalSeconds:  types.Int64Value(30),
		FailoverMode:     types.StringValue(infra.FailoverModeOptimistic),
	}

	spec, diags := asyncDiskReplicaToSpec(context.Background(), model)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("spec should be valid: %v", err)
	}
	want := infra.AsyncDiskReplicaSpec{
		DiskID:           "disk-1",
		TargetNodePoolID: "pool-b",
		IntervalSeconds:  30,
		FailoverPolicy:   infra.FailoverPolicy{Mode: infra.FailoverModeOptimistic},
	}
	if spec != want {
		t.Fatalf("toSpec() = %+v, want %+v", spec, want)
	}

	r := &infra.AsyncDiskReplica{Metadata: infra.ObjectMeta{UID: "replica-1"}, Spec: spec}
	r.Status.NodeName = "node-b"
	r.Status.LastSyncedAt = "2026-10-01T12:00:00Z"
	r.Status.BytesSynced = 1024
	r.Status.Phase = infra.PhaseReady

	got, diags := asyncDiskReplicaToModel(context.Background(), r)
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if got.NodeName.ValueString() != "node-b" || got.BytesSynced.ValueInt64() != 1024 {
		t.Fatalf("toModel() status fields = %+v", got)
	}
	if got.DiskID.ValueString() != model.DiskID.ValueString() || got.IntervalSeconds.ValueInt64() != model.IntervalSeconds.ValueInt64() {
		t.Fatalf("toModel() did not round-trip the spec: %+v", got)
	}
}

func TestAsyncDiskReplicaToModelDefaultsTheFailoverModeAndInterval(t *testing.T) {
	t.Parallel()

	// An unset FailoverPolicy.Mode and IntervalSeconds should read back as
	// their effective defaults, not blank/zero, so a plan doesn't see drift
	// on every read.
	r := &infra.AsyncDiskReplica{
		Metadata: infra.ObjectMeta{UID: "replica-1"},
		Spec:     infra.AsyncDiskReplicaSpec{DiskID: "disk-1"},
	}

	got, diags := asyncDiskReplicaToModel(context.Background(), r)
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if got.FailoverMode.ValueString() != infra.FailoverModeConfirmed {
		t.Fatalf("failover_mode = %q, want %q", got.FailoverMode.ValueString(), infra.FailoverModeConfirmed)
	}
	if got.IntervalSeconds.ValueInt64() != infra.DefaultAsyncReplicaIntervalSeconds {
		t.Fatalf("interval_seconds = %d, want %d", got.IntervalSeconds.ValueInt64(), infra.DefaultAsyncReplicaIntervalSeconds)
	}
}
