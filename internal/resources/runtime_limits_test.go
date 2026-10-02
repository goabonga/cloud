// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestTerraformRejectsExplicitUnlimitedRuntimeLimits(t *testing.T) {
	cpu := runtimeFloatRange{min: 0.01, max: 64}
	for _, value := range []types.Float64{types.Float64Value(0), types.Float64Value(65), types.Float64Null(), types.Float64Unknown(), types.Float64Value(1)} {
		var out validator.Float64Response
		cpu.ValidateFloat64(t.Context(), validator.Float64Request{Path: path.Root("cpu"), ConfigValue: value}, &out)
		bad := !value.IsNull() && !value.IsUnknown() && (value.ValueFloat64() == 0 || value.ValueFloat64() == 65)
		if out.Diagnostics.HasError() != bad {
			t.Fatalf("CPU %v: %v", value, out.Diagnostics)
		}
	}
	pids := runtimeIntRange{min: 1, max: 4096}
	for _, value := range []types.Int64{types.Int64Value(0), types.Int64Value(4097), types.Int64Null(), types.Int64Unknown(), types.Int64Value(256)} {
		var out validator.Int64Response
		pids.ValidateInt64(t.Context(), validator.Int64Request{Path: path.Root("pids_max"), ConfigValue: value}, &out)
		bad := !value.IsNull() && !value.IsUnknown() && (value.ValueInt64() == 0 || value.ValueInt64() == 4097)
		if out.Diagnostics.HasError() != bad {
			t.Fatalf("pids %v: %v", value, out.Diagnostics)
		}
	}
}
