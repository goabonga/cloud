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

func TestMicroVMResourceMetadata(t *testing.T) {
	t.Parallel()

	var resp resource.MetadataResponse
	NewMicroVMResource().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "infra"}, &resp)
	if resp.TypeName != "infra_microvm" {
		t.Fatalf("TypeName = %q, want infra_microvm", resp.TypeName)
	}
}

func TestMicroVMResourceSchema(t *testing.T) {
	t.Parallel()

	var resp resource.SchemaResponse
	NewMicroVMResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	attrs := resp.Schema.Attributes

	for _, required := range []string{"subnet_id", "vcpus", "memory_mb", "kernel_path", "image"} {
		if !attrs[required].IsRequired() {
			t.Fatalf("%s should be required", required)
		}
	}
	for _, computed := range []string{"id", "ip", "tap", "pid", "ready", "phase"} {
		if !attrs[computed].IsComputed() {
			t.Fatalf("%s should be computed", computed)
		}
	}
}

func TestMicroVMToSpecAndBack(t *testing.T) {
	t.Parallel()

	model := microvmModel{
		ID:               types.StringValue("vm-1"),
		Name:             types.StringValue("web-1"),
		SubnetID:         types.StringValue("sn-1"),
		SecurityGroupID:  types.StringValue("sg-1"),
		Hostname:         types.StringValue("web-1"),
		VCPUs:            types.Int64Value(2),
		MemoryMB:         types.Int64Value(1024),
		KernelPath:       types.StringValue("/boot/vmlinux"),
		InitrdPath:       types.StringValue("/boot/initrd"),
		CmdLine:          types.StringValue("console=ttyS0"),
		Image:            types.StringValue("https://example.invalid/base.raw"),
		SSHAuthorizedKey: types.StringValue("ssh-ed25519 AAAA key"),
		UserData:         types.StringValue("#cloud-config\n"),
	}

	spec, diags := microvmToSpec(context.Background(), model)
	if diags.HasError() {
		t.Fatalf("toSpec: %v", diags)
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("spec should be valid: %v", err)
	}
	want := infra.MicroVMSpec{
		Name:             "web-1",
		SubnetID:         "sn-1",
		SecurityGroupID:  "sg-1",
		Hostname:         "web-1",
		VCPUs:            2,
		MemoryMB:         1024,
		KernelPath:       "/boot/vmlinux",
		InitrdPath:       "/boot/initrd",
		CmdLine:          "console=ttyS0",
		Image:            "https://example.invalid/base.raw",
		SSHAuthorizedKey: "ssh-ed25519 AAAA key",
		UserData:         "#cloud-config\n",
	}
	if spec != want {
		t.Fatalf("toSpec() = %+v, want %+v", spec, want)
	}

	r := &infra.MicroVM{Metadata: infra.ObjectMeta{UID: "vm-1"}, Spec: spec}
	r.Status.IP = "10.0.1.10"
	r.Status.Tap = "tap-abcd1234"
	r.Status.Pid = 4242
	r.Status.Ready = true
	r.Status.Phase = infra.PhaseReady

	got, diags := microvmToModel(context.Background(), r)
	if diags.HasError() {
		t.Fatalf("toModel: %v", diags)
	}
	if got.ID.ValueString() != "vm-1" || got.IP.ValueString() != "10.0.1.10" || got.Tap.ValueString() != "tap-abcd1234" {
		t.Fatalf("toModel() status fields = %+v", got)
	}
	if got.Pid.ValueInt64() != 4242 || !got.Ready.ValueBool() || got.Phase.ValueString() != "Ready" {
		t.Fatalf("toModel() status fields = %+v", got)
	}
	if got.SubnetID.ValueString() != model.SubnetID.ValueString() || got.VCPUs.ValueInt64() != model.VCPUs.ValueInt64() {
		t.Fatalf("toModel() did not round-trip the spec: %+v", got)
	}
}
