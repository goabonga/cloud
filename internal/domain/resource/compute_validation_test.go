// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>
package resource

import "testing"

func TestComputeRejectsShellInputs(t *testing.T) {
	for _, value := range []string{"x;id", "$(id)", "x\ncommand", "-x"} {
		spec := ComputeSpec{SubnetID: "s", Image: "i", Hostname: value}
		if spec.Validate() == nil {
			t.Fatalf("accepted hostname %q", value)
		}
	}
	for _, key := range []string{"x;id", "$(id)", "A=B", "1BAD"} {
		spec := ComputeSpec{SubnetID: "s", Image: "i", Env: map[string]string{key: "value"}}
		if spec.Validate() == nil {
			t.Fatalf("accepted environment key %q", key)
		}
	}
	if err := (ComputeSpec{SubnetID: "s", Image: "i", Hostname: "worker-1.example", Env: map[string]string{"HTTP_PORT": "80"}}).Validate(); err != nil {
		t.Fatal(err)
	}
}
