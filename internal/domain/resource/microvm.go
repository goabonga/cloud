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
	// NodePoolID constrains placement to a node pool; empty schedules
	// anywhere.
	NodePoolID string `json:"nodePoolId,omitempty"`
	Hostname   string `json:"hostname,omitempty"`
	VCPUs      int    `json:"vcpus"`
	MemoryMB   int    `json:"memoryMb"`
	// KernelPath is an absolute path to an uncompressed Linux kernel readable
	// by the agent's host. There is no kernel fetch/caching: this must
	// already exist on the node.
	KernelPath string `json:"kernelPath"`
	// InitrdPath is an optional absolute path to an initramfs.
	InitrdPath string `json:"initrdPath,omitempty"`
	// CmdLine is appended to the kernel command line. The agent also appends
	// a static "ip=" directive derived from the allocated address and a
	// "ds=nocloud-net" directive pointing cloud-init at its seed server, so
	// the guest configures eth0 and runs cloud-init without either being
	// baked into the image.
	CmdLine string `json:"cmdLine,omitempty"`
	// Image is the boot disk's source: an http(s) URL fetched once into a
	// node-local cache, or an absolute path on the agent's host copied into
	// it. Each instance gets its own copy-on-write clone of the cached image,
	// so instances never share writable state and the cache is never
	// mutated.
	Image string `json:"image"`
	// SSHAuthorizedKey, when set, is written to the guest's cloud-init
	// cloud-config as its sole authorized key.
	SSHAuthorizedKey string `json:"sshAuthorizedKey,omitempty"`
	// UserData, when set, is used verbatim as the guest's cloud-init
	// user-data (a "#cloud-config" document or a "#!" script), overriding
	// the minimal cloud-config the agent would otherwise generate from
	// hostname and sshAuthorizedKey.
	UserData string `json:"userData,omitempty"`
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
	if s.Image == "" {
		return fmt.Errorf("microvm: image is required")
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
	// NodeName is the node the scheduler assigned; empty means either not
	// yet scheduled, or (with no scheduler running) "realize on every
	// agent", the same single-host fallback compute uses.
	NodeName string `json:"nodeName,omitempty"`
}

// MicroVM is a micro-VM resource.
type MicroVM = Resource[MicroVMSpec, MicroVMStatus]
