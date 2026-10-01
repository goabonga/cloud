// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

//go:build integration

package integration

import (
	"os"
	"testing"

	"github.com/goabonga/infrastructure/internal/hypervisor/kvm"
)

// kvmCapUserMemory is KVM_CAP_USER_MEMORY (linux/kvm.h:723), required for
// KVM_SET_USER_MEMORY_REGION-based guest memory, which every later
// milestone depends on — a host reporting it unsupported means this whole
// effort cannot work there.
const kvmCapUserMemory = 3

// TestKVMOpen opens /dev/kvm and checks the host's API version and its
// support for user-memory-backed guest RAM. It needs root (or kvm-group
// membership) and a KVM-capable host; see docs/architecture/go-hypervisor.md.
func TestKVMOpen(t *testing.T) {
	if _, err := os.Stat(kvm.DevicePath); err != nil {
		t.Skipf("%s not available: %v", kvm.DevicePath, err)
	}

	dev, err := kvm.Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer dev.Close()

	version, err := dev.APIVersion()
	if err != nil {
		t.Fatalf("APIVersion: %v", err)
	}
	if version != 12 {
		t.Fatalf("KVM_GET_API_VERSION = %d, want 12 (stable KVM API)", version)
	}

	support, err := dev.CheckExtension(kvmCapUserMemory)
	if err != nil {
		t.Fatalf("CheckExtension(KVM_CAP_USER_MEMORY): %v", err)
	}
	if support == 0 {
		t.Fatal("host does not support KVM_CAP_USER_MEMORY")
	}
}
