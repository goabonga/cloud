// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	infra "github.com/goabonga/infrastructure/internal/domain/resource"
)

func TestFunctionResourceMetadataAndSchema(t *testing.T) {
	t.Parallel()

	var meta resource.MetadataResponse
	NewFunctionResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "infra"}, &meta)
	if meta.TypeName != "infra_function" {
		t.Fatalf("TypeName = %q, want infra_function", meta.TypeName)
	}

	var sresp resource.SchemaResponse
	NewFunctionResource().Schema(context.Background(), resource.SchemaRequest{}, &sresp)
	for _, required := range []string{"subnet_id", "image", "port"} {
		if !sresp.Schema.Attributes[required].IsRequired() {
			t.Fatalf("%s should be required", required)
		}
	}
	for _, computed := range []string{"id", "phase", "warm_count"} {
		if !sresp.Schema.Attributes[computed].IsComputed() {
			t.Fatalf("%s should be computed", computed)
		}
	}
}

func TestFunctionToSpecAndBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	env, diags := types.MapValueFrom(ctx, types.StringType, map[string]string{"FOO": "bar"})
	if diags.HasError() {
		t.Fatalf("build env: %v", diags)
	}
	warmPool, diags := types.ObjectValueFrom(ctx, warmPoolAttrTypes, warmPoolModel{
		MinWarm:        types.Int64Value(1),
		MaxWarm:        types.Int64Value(5),
		IdleTTLSeconds: types.Int64Value(30),
		AllowColdStart: types.BoolValue(true),
	})
	if diags.HasError() {
		t.Fatalf("build warm_pool: %v", diags)
	}

	model := functionModel{
		ID:              types.StringValue("fn-1"),
		Name:            types.StringValue("resize"),
		SubnetID:        types.StringValue("subnet-1"),
		SecurityGroupID: types.StringValue("sg-1"),
		NodePoolID:      types.StringValue("pool-1"),
		CPU:             types.Float64Value(0.5),
		MemoryMB:        types.Int64Value(256),
		PidsMax:         types.Int64Value(32),
		Image:           types.StringValue("resize:latest"),
		Command:         types.StringValue("/run.sh"),
		Env:             env,
		Port:            types.Int64Value(8080),
		WarmPool:        warmPool,
	}

	spec, diags := functionToSpec(ctx, model)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	want := infra.FunctionSpec{
		Name: "resize", SubnetID: "subnet-1", SecurityGroupID: "sg-1", NodePoolID: "pool-1",
		CPU: 0.5, MemoryMB: 256, PidsMax: 32,
		Image: "resize:latest", Command: "/run.sh",
		Env:  map[string]string{"FOO": "bar"},
		Port: 8080,
		WarmPool: infra.WarmPoolPolicy{
			MinWarm: 1, MaxWarm: 5, IdleTTLSeconds: 30, AllowColdStart: true,
		},
	}
	if !reflect.DeepEqual(spec, want) {
		t.Fatalf("toSpec() = %+v, want %+v", spec, want)
	}

	r := &infra.Function{Metadata: infra.ObjectMeta{UID: "fn-1"}, Spec: spec}
	r.Status.WarmCount = 2
	r.Status.Phase = infra.PhaseReady

	got, diags := functionToModel(ctx, r)
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if got.WarmCount.ValueInt64() != 2 || got.Phase.ValueString() != "Ready" {
		t.Fatalf("toModel() status fields = %+v", got)
	}
	if got.SubnetID.ValueString() != model.SubnetID.ValueString() || got.Port.ValueInt64() != model.Port.ValueInt64() {
		t.Fatalf("toModel() did not round-trip the spec: %+v", got)
	}

	backToSpec, diags := functionToSpec(ctx, got)
	if diags.HasError() {
		t.Fatalf("toSpec (round 2): %v", diags)
	}
	if !reflect.DeepEqual(backToSpec, spec) {
		t.Fatalf("round trip = %+v, want %+v", backToSpec, spec)
	}

	id := NewFunctionResource().(*genericResource[functionModel, infra.FunctionSpec, infra.FunctionStatus]).def.id
	if id(got) != "fn-1" {
		t.Fatalf("id() = %q, want fn-1", id(got))
	}
}

func TestFunctionToSpecWithoutEnvOrWarmPool(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	model := functionModel{
		SubnetID: types.StringValue("subnet-1"),
		Image:    types.StringValue("fn:latest"),
		Port:     types.Int64Value(8080),
		Env:      types.MapNull(types.StringType),
		WarmPool: types.ObjectNull(warmPoolAttrTypes),
	}
	spec, diags := functionToSpec(ctx, model)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	if spec.Env != nil {
		t.Fatalf("env = %v, want nil when left out", spec.Env)
	}
	if spec.WarmPool != (infra.WarmPoolPolicy{}) {
		t.Fatalf("warm_pool = %+v, want the zero value when left out", spec.WarmPool)
	}
}
