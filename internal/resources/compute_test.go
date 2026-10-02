// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

func computeSchema(t *testing.T) schema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	NewComputeResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	return resp.Schema
}

// planString runs a string attribute's plan modifiers from state to plan, the
// way Terraform does on an update, and reports whether they replace it.
func planString(t *testing.T, mods []planmodifier.String, state, config, plan types.String) bool {
	t.Helper()
	// A non-null state and plan mark an update, not a create or a destroy.
	raw := tftypes.NewValue(tftypes.Object{}, map[string]tftypes.Value{})
	req := planmodifier.StringRequest{
		StateValue: state, ConfigValue: config, PlanValue: plan,
		State: tfsdk.State{Raw: raw}, Plan: tfsdk.Plan{Raw: raw},
	}
	resp := &planmodifier.StringResponse{PlanValue: plan}
	for _, m := range mods {
		req.PlanValue = resp.PlanValue
		m.PlanModifyString(context.Background(), req, resp)
	}
	return resp.RequiresReplace
}

func TestComputeChangesReplaceTheInstance(t *testing.T) {
	t.Parallel()

	attrs := computeSchema(t).Attributes
	mods := attrs["image"].(schema.StringAttribute).PlanModifiers
	if !planString(t, mods, types.StringValue("nginx:1"), types.StringValue("nginx:2"), types.StringValue("nginx:2")) {
		t.Fatal("a new image must replace the instance")
	}
	if planString(t, mods, types.StringValue("nginx:1"), types.StringValue("nginx:1"), types.StringValue("nginx:1")) {
		t.Fatal("an unchanged image must not replace the instance")
	}
	// command left out of the configuration: its planned value is unknown
	// on any update, and must read as the state's, not as a change.
	cmd := attrs["command"].(schema.StringAttribute).PlanModifiers
	if planString(t, cmd, types.StringValue("nginx"), types.StringNull(), types.StringUnknown()) {
		t.Fatal("an omitted command must keep its state value")
	}
}

func TestEveryComputeSettingButTheNameReplacesTheInstance(t *testing.T) {
	t.Parallel()

	for name, attr := range computeSchema(t).Attributes {
		if name == "name" || (!attr.IsRequired() && !attr.IsOptional()) {
			continue
		}
		var n int
		switch a := attr.(type) {
		case schema.StringAttribute:
			n = len(a.PlanModifiers)
		case schema.Float64Attribute:
			n = len(a.PlanModifiers)
		case schema.Int64Attribute:
			n = len(a.PlanModifiers)
		case schema.BoolAttribute:
			n = len(a.PlanModifiers)
		case schema.MapAttribute:
			n = len(a.PlanModifiers)
		case schema.ListAttribute:
			n = len(a.PlanModifiers)
		case schema.ListNestedAttribute:
			n = len(a.PlanModifiers)
		default:
			t.Fatalf("%s: unexpected attribute type %T", name, attr)
		}
		if n == 0 {
			t.Errorf("%s: a change must replace the instance", name)
		}
	}
}

func TestDiskSizeAndKeyChangesReplaceTheDisk(t *testing.T) {
	t.Parallel()

	var resp resource.SchemaResponse
	NewDiskResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if len(resp.Schema.Attributes["size_mb"].(schema.Int64Attribute).PlanModifiers) == 0 ||
		len(resp.Schema.Attributes["kms_key_id"].(schema.StringAttribute).PlanModifiers) == 0 {
		t.Fatal("a new size or key must replace the disk: the agent creates its image once")
	}
}

func TestComputeModelRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	spec := infra.ComputeSpec{
		Name: "web-1", SubnetID: "sn-1", SecurityGroupID: "sg-1", NodePoolID: "pool-a",
		Hostname: "web-1", CPU: 1.5, MemoryMB: 1024, PidsMax: 100,
		Image: "nginx:1", Command: "/run.sh",
		Env:        map[string]string{"FOO": "bar"},
		Ports:      []string{"80/tcp", "443/tcp"},
		Disks:      []infra.ComputeDiskRef{{DiskID: "disk-1", MountPath: "/data", ReadOnly: true}},
		Privileged: true,
	}
	r := &infra.Compute{Metadata: infra.ObjectMeta{UID: "compute-1"}, Spec: spec}
	r.Status.IP = "10.0.0.5"
	r.Status.Ready = true
	r.Status.Phase = infra.PhaseReady

	m, diags := computeToModel(ctx, r)
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if m.ID.ValueString() != "compute-1" || m.IP.ValueString() != "10.0.0.5" || !m.Ready.ValueBool() || m.Phase.ValueString() != "Ready" {
		t.Fatalf("toModel() status fields = %+v", m)
	}

	got, diags := computeToSpec(ctx, m)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	if !reflect.DeepEqual(got, spec) {
		t.Fatalf("round trip = %+v, want %+v", got, spec)
	}

	id := NewComputeResource().(*genericResource[computeModel, infra.ComputeSpec, infra.ComputeStatus]).def.id
	if id(m) != "compute-1" {
		t.Fatalf("id() = %q, want compute-1", id(m))
	}
}
