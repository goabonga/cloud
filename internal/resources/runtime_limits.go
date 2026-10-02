// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resources

import (
	"context"
	"fmt"
	"math"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

type runtimeFloatRange struct{ min, max float64 }

func (v runtimeFloatRange) Description(context.Context) string {
	return fmt.Sprintf("must be between %g and %g; omit the attribute to use its default", v.min, v.max)
}
func (v runtimeFloatRange) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }
func (v runtimeFloatRange) ValidateFloat64(ctx context.Context, req validator.Float64Request, resp *validator.Float64Response) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := req.ConfigValue.ValueFloat64()
	if math.IsNaN(value) || math.IsInf(value, 0) || value < v.min || value > v.max {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Runtime Limit", v.Description(ctx))
	}
}

type runtimeIntRange struct{ min, max int64 }

func (v runtimeIntRange) Description(context.Context) string {
	return fmt.Sprintf("must be between %d and %d; omit the attribute to use its default", v.min, v.max)
}
func (v runtimeIntRange) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }
func (v runtimeIntRange) ValidateInt64(ctx context.Context, req validator.Int64Request, resp *validator.Int64Response) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := req.ConfigValue.ValueInt64()
	if value < v.min || value > v.max {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Runtime Limit", v.Description(ctx))
	}
}
