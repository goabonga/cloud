// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

type asyncDiskReplicaModel struct {
	ID               types.String `tfsdk:"id"`
	DiskID           types.String `tfsdk:"disk_id"`
	TargetNodePoolID types.String `tfsdk:"target_node_pool_id"`
	IntervalSeconds  types.Int64  `tfsdk:"interval_seconds"`
	FailoverMode     types.String `tfsdk:"failover_mode"`
	NodeName         types.String `tfsdk:"node_name"`
	LastSyncedAt     types.String `tfsdk:"last_synced_at"`
	BytesSynced      types.Int64  `tfsdk:"bytes_synced"`
	Phase            types.String `tfsdk:"phase"`
}

// NewAsyncDiskReplicaResource is the infra_async_disk_replica resource
// factory.
func NewAsyncDiskReplicaResource() resource.Resource {
	return newGeneric(resourceDef[asyncDiskReplicaModel, infra.AsyncDiskReplicaSpec, infra.AsyncDiskReplicaStatus]{
		kind: infra.KindAsyncDiskReplica,
		schema: schema.Schema{
			MarkdownDescription: "A periodic, best-effort copy of a disk's backing file onto a second node, " +
				"for recovering it after the disk's own node is lost. Attach nothing to a disk for no replication.",
			Attributes: map[string]schema.Attribute{
				"id":                  idAttribute(),
				"disk_id":             schema.StringAttribute{Required: true, PlanModifiers: replaceString(), MarkdownDescription: "Disk to replicate; changing it replaces the replica."},
				"target_node_pool_id": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: replaceString(), MarkdownDescription: "Node pool to schedule the replica onto; empty allows any node other than the disk's own."},
				"interval_seconds":    schema.Int64Attribute{Optional: true, Computed: true, MarkdownDescription: "How often the replica re-syncs; defaults to 60."},
				"failover_mode":       schema.StringAttribute{Optional: true, Computed: true, MarkdownDescription: "`optimistic` or `confirmed` (default); see the disk-replication architecture docs."},
				"node_name":           schema.StringAttribute{Computed: true, MarkdownDescription: "Node holding this replica's copy, assigned by the scheduler."},
				"last_synced_at":      schema.StringAttribute{Computed: true, MarkdownDescription: "When the copy last completed, RFC3339."},
				"bytes_synced":        schema.Int64Attribute{Computed: true, MarkdownDescription: "Size of the copy as of last_synced_at."},
				"phase":               phaseAttribute(),
			},
		},
		toSpec:  asyncDiskReplicaToSpec,
		toModel: asyncDiskReplicaToModel,
		id:      func(m asyncDiskReplicaModel) string { return m.ID.ValueString() },
	})
}

func asyncDiskReplicaToSpec(_ context.Context, m asyncDiskReplicaModel) (infra.AsyncDiskReplicaSpec, diag.Diagnostics) {
	return infra.AsyncDiskReplicaSpec{
		DiskID:           m.DiskID.ValueString(),
		TargetNodePoolID: m.TargetNodePoolID.ValueString(),
		IntervalSeconds:  int(m.IntervalSeconds.ValueInt64()),
		FailoverPolicy:   infra.FailoverPolicy{Mode: m.FailoverMode.ValueString()},
	}, nil
}

func asyncDiskReplicaToModel(_ context.Context, r *infra.AsyncDiskReplica) (asyncDiskReplicaModel, diag.Diagnostics) {
	return asyncDiskReplicaModel{
		ID:               types.StringValue(r.Metadata.UID),
		DiskID:           types.StringValue(r.Spec.DiskID),
		TargetNodePoolID: types.StringValue(r.Spec.TargetNodePoolID),
		IntervalSeconds:  types.Int64Value(int64(r.Spec.IntervalOrDefault())),
		FailoverMode:     types.StringValue(r.Spec.FailoverPolicy.EffectiveMode()),
		NodeName:         types.StringValue(r.Status.NodeName),
		LastSyncedAt:     types.StringValue(r.Status.LastSyncedAt),
		BytesSynced:      types.Int64Value(r.Status.BytesSynced),
		Phase:            types.StringValue(string(r.Status.Phase)),
	}, nil
}
