// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package main

import (
	"fmt"

	"github.com/goabonga/infrastructure/internal/hypervisor/protocol"
)

// maxCreateVCPUs mirrors boot.BuildMPTable's own cap (the Intel MP
// Specification's 8-bit APIC ID, one value reserved for the IOAPIC) —
// checked again here so a request asking for too many vcpus fails fast,
// with a clear message, before this process ever opens /dev/kvm, rather
// than surfacing from deep inside loadGuest after most of a VM's setup
// has already run.
const maxCreateVCPUs = 254

// validateCreateParams rejects a create request before hypervisor.New
// ever touches /dev/kvm or the filesystem beyond what reading these
// fields themselves requires — internal/hypervisor's own Config
// validation (VCPUs/MemoryMB) still applies too, for any caller that
// doesn't go through this protocol layer; this is the fast,
// clearly-worded pre-flight check for the one that does.
func validateCreateParams(p protocol.CreateParams) error {
	if p.VCPUs < 1 {
		return fmt.Errorf("vcpus must be positive, got %d", p.VCPUs)
	}
	if p.VCPUs > maxCreateVCPUs {
		return fmt.Errorf("vcpus must be at most %d, got %d", maxCreateVCPUs, p.VCPUs)
	}
	if p.MemoryMB <= 0 {
		return fmt.Errorf("memory_mb must be positive, got %d", p.MemoryMB)
	}
	if p.KernelPath == "" {
		return fmt.Errorf("kernel_path is required")
	}
	return nil
}
