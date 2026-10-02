// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import (
	"math"
	"testing"
)

func TestRuntimeSizingDefaultsAndCeilings(t *testing.T) {
	base := ComputeSpec{SubnetID: "subnet", Image: "image"}
	defaults := base.WithDefaults()
	if defaults.CPU != 1 || defaults.MemoryMB != 256 || defaults.PidsMax != 256 {
		t.Fatalf("unbounded defaults: %+v", defaults)
	}
	invalid := []ComputeSpec{{CPU: math.NaN()}, {CPU: math.Inf(1)}, {CPU: 0.001}, {CPU: MaxRuntimeCPU + 1}, {MemoryMB: MaxRuntimeMemoryMB + 1}, {PidsMax: -1}, {PidsMax: MaxRuntimePids + 1}}
	for _, shape := range invalid {
		shape.SubnetID, shape.Image = "subnet", "image"
		if shape.Validate() == nil {
			t.Fatalf("invalid compute shape accepted: %+v", shape)
		}
		fn := FunctionSpec{SubnetID: "subnet", Image: "image", Port: 8080, CPU: shape.CPU, MemoryMB: shape.MemoryMB, PidsMax: shape.PidsMax}
		if fn.Validate() == nil {
			t.Fatalf("invalid function shape accepted: %+v", fn)
		}
	}
	explicit := ComputeSpec{CPU: 2, MemoryMB: 512, PidsMax: 128}.WithDefaults()
	if explicit.CPU != 2 || explicit.MemoryMB != 512 || explicit.PidsMax != 128 {
		t.Fatal("explicit limits were replaced")
	}
	fn := (FunctionSpec{SubnetID: "subnet", Image: "image", Port: 8080}).WithDefaults()
	if fn.CPU != 1 || fn.MemoryMB != 256 || fn.PidsMax != 256 || fn.WarmPool.MaxWarm != 32 {
		t.Fatalf("function defaults: %+v", fn)
	}
	if (WarmPoolPolicy{MinWarm: 257}).Validate() == nil || (WarmPoolPolicy{MaxWarm: 257}).Validate() == nil {
		t.Fatal("unbounded warm pool accepted")
	}
	if (MicroVMSpec{SubnetID: "subnet", KernelPath: "kernel", Image: "image", VCPUs: MaxRuntimeCPU + 1, MemoryMB: 512}).Validate() == nil {
		t.Fatal("unbounded VM sizing accepted")
	}
	if (DiskSpec{SizeMB: (1 << 20) + 1}).Validate() == nil {
		t.Fatal("unbounded disk sizing accepted")
	}
}
