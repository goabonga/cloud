// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "fmt"

// KindMicroVM is the resource kind for cloud-hypervisor micro-VMs.
const KindMicroVM = "microvm"

// MicroVMFinalizer is attached by the agent so the cloud-hypervisor process,
// TAP device and firewall rules are torn down before the record is removed.
const MicroVMFinalizer = "infra.io/microvm"

// MicroVMSpec is the desired state of a micro-VM: a kernel and a disk image
// booted under cloud-hypervisor, attached to a subnet.
type MicroVMSpec struct {
	Name            string `json:"name,omitempty"`
	SubnetID        string `json:"subnetId"`
	SecurityGroupID string `json:"securityGroupId,omitempty"`
	Hostname        string `json:"hostname,omitempty"`
	VCPUs           int    `json:"vcpus"`
	MemoryMB        int    `json:"memoryMb"`
	// KernelPath is an absolute path to an uncompressed Linux kernel readable
	// by the agent's host. Image fetch/caching is not implemented yet: this
	// must already exist on the node.
	KernelPath string `json:"kernelPath"`
	// InitrdPath is an optional absolute path to an initramfs.
	InitrdPath string `json:"initrdPath,omitempty"`
	// CmdLine is appended to the kernel command line. The agent prepends a
	// static "ip=" directive derived from the allocated address so the guest
	// configures eth0 at boot without cloud-init.
	CmdLine string `json:"cmdLine,omitempty"`
	// BootImagePath is an absolute path to a raw disk image readable by the
	// agent's host. Image fetch/caching is not implemented yet: this must
	// already exist on the node, and is used in place, not copied.
	BootImagePath string `json:"bootImagePath"`
}

// Validate reports whether the spec is well-formed.
func (s MicroVMSpec) Validate() error {
	if s.SubnetID == "" {
		return fmt.Errorf("microvm: subnetId is required")
	}
	if s.VCPUs <= 0 {
		return fmt.Errorf("microvm: vcpus must be positive")
	}
	if s.MemoryMB <= 0 {
		return fmt.Errorf("microvm: memoryMb must be positive")
	}
	if s.KernelPath == "" {
		return fmt.Errorf("microvm: kernelPath is required")
	}
	if s.BootImagePath == "" {
		return fmt.Errorf("microvm: bootImagePath is required")
	}
	return nil
}

// MicroVMStatus is the observed state of a micro-VM.
type MicroVMStatus struct {
	StatusBase
	IP  string `json:"ip,omitempty"`
	Tap string `json:"tap,omitempty"`
	Pid int    `json:"pid,omitempty"`
	// Ready mirrors StatusBase.Phase == PhaseReady for API consumers that read
	// the flag directly, matching ComputeStatus.
	Ready bool `json:"ready"`
	// NodeName is populated once a scheduler assigns placement (not yet
	// implemented for microvm); empty means "realize on every agent", the
	// same single-host fallback compute uses.
	NodeName string `json:"nodeName,omitempty"`
}

// MicroVM is a micro-VM resource.
type MicroVM = Resource[MicroVMSpec, MicroVMStatus]
