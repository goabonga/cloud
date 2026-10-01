// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package main

import (
	"testing"

	"github.com/goabonga/infrastructure/internal/hypervisor/protocol"
)

func validParams() protocol.CreateParams {
	return protocol.CreateParams{VCPUs: 1, MemoryMB: 256, KernelPath: "/boot/vmlinuz"}
}

func TestValidateCreateParamsAccepts(t *testing.T) {
	if err := validateCreateParams(validParams()); err != nil {
		t.Errorf("validateCreateParams(valid) = %v, want nil", err)
	}
}

func TestValidateCreateParamsRejectsZeroVCPUs(t *testing.T) {
	p := validParams()
	p.VCPUs = 0
	if err := validateCreateParams(p); err == nil {
		t.Error("validateCreateParams accepted 0 vcpus, want error")
	}
}

func TestValidateCreateParamsRejectsNegativeVCPUs(t *testing.T) {
	p := validParams()
	p.VCPUs = -1
	if err := validateCreateParams(p); err == nil {
		t.Error("validateCreateParams accepted -1 vcpus, want error")
	}
}

func TestValidateCreateParamsRejectsTooManyVCPUs(t *testing.T) {
	p := validParams()
	p.VCPUs = maxCreateVCPUs + 1
	if err := validateCreateParams(p); err == nil {
		t.Errorf("validateCreateParams accepted %d vcpus, want error", p.VCPUs)
	}

	p.VCPUs = maxCreateVCPUs
	if err := validateCreateParams(p); err != nil {
		t.Errorf("validateCreateParams(%d vcpus, the cap) = %v, want nil", p.VCPUs, err)
	}
}

func TestValidateCreateParamsRejectsNonPositiveMemory(t *testing.T) {
	p := validParams()
	p.MemoryMB = 0
	if err := validateCreateParams(p); err == nil {
		t.Error("validateCreateParams accepted memory_mb=0, want error")
	}
}

func TestValidateCreateParamsRejectsMissingKernelPath(t *testing.T) {
	p := validParams()
	p.KernelPath = ""
	if err := validateCreateParams(p); err == nil {
		t.Error("validateCreateParams accepted an empty kernel_path, want error")
	}
}
