// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// KindCompute is the resource kind for compute instances.
const KindCompute = "compute"

// FunctionPortRangeLo and Hi are reserved for controller-created function slots.
const (
	FunctionPortRangeLo = 30000
	FunctionPortRangeHi = 32767
)

// ComputeFinalizer is attached by the agent so the network namespace, veth pair,
// cgroup, rootfs and firewall rules are torn down before the record is removed.
const ComputeFinalizer = "infra.io/compute"

// ComputeDiskRef attaches a disk to a compute instance at a mount path.
type ComputeDiskRef struct {
	DiskID    string `json:"diskId"`
	MountPath string `json:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

// ComputeSpec is the desired state of a compute instance: an OCI image run in a
// network namespace with cgroup limits, attached to a subnet and disks.
type ComputeSpec struct {
	Name            string `json:"name,omitempty"`
	SubnetID        string `json:"subnetId"`
	SecurityGroupID string `json:"securityGroupId,omitempty"`
	// NodePoolID constrains placement to a node pool; empty schedules anywhere.
	NodePoolID string            `json:"nodePoolId,omitempty"`
	Hostname   string            `json:"hostname,omitempty"`
	CPU        float64           `json:"cpu,omitempty"`
	MemoryMB   int               `json:"memoryMb,omitempty"`
	PidsMax    int               `json:"pidsMax,omitempty"`
	Image      string            `json:"image"`
	Command    string            `json:"command,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Ports      []string          `json:"ports,omitempty"`
	Disks      []ComputeDiskRef  `json:"disks,omitempty"`
	Privileged bool              `json:"privileged,omitempty"`
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var hostName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$`)

// Validate reports whether the spec is well-formed.
func (s ComputeSpec) Validate() error {
	if s.Hostname != "" && !hostName.MatchString(s.Hostname) {
		return fmt.Errorf("compute: invalid hostname")
	}
	for key := range s.Env {
		if !envName.MatchString(key) {
			return fmt.Errorf("compute: invalid environment name %q", key)
		}
	}
	if s.SubnetID == "" {
		return fmt.Errorf("compute: subnetId is required")
	}
	if s.Image == "" {
		return fmt.Errorf("compute: image is required")
	}
	if err := s.ValidateRuntimeLimits(); err != nil {
		return err
	}
	for _, mapping := range s.Ports {
		host, _, _ := strings.Cut(mapping, ":")
		port, err := strconv.Atoi(host)
		if err == nil && port >= FunctionPortRangeLo && port <= FunctionPortRangeHi {
			return fmt.Errorf("compute: host ports %d-%d are reserved for functions", FunctionPortRangeLo, FunctionPortRangeHi)
		}
	}
	for i, d := range s.Disks {
		if d.DiskID == "" || d.MountPath == "" {
			return fmt.Errorf("compute: disk %d requires diskId and mountPath", i)
		}
	}
	return nil
}

// ComputeStatus is the observed state of a compute instance.
type ComputeStatus struct {
	StatusBase
	IP        string `json:"ip,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	// VethHost is the bridge-side veth interface name, recorded so teardown can
	// remove it without re-deriving the topology.
	VethHost string `json:"vethHost,omitempty"`
	// Rootfs is the extracted OCI rootfs path, recorded for teardown.
	Rootfs   string `json:"rootfs,omitempty"`
	Ready    bool   `json:"ready"`
	NodeName string `json:"nodeName,omitempty"`
}

// Compute is a compute-instance resource.
type Compute = Resource[ComputeSpec, ComputeStatus]

// Runtime admission ceilings apply equally to compute and function shapes.
const (
	MaxRuntimeCPU      = 64
	MaxRuntimeMemoryMB = 262144
	MaxRuntimePids     = 4096
)

// WithDefaults gives omitted runtime limits a finite cgroup budget.
func (s ComputeSpec) WithDefaults() ComputeSpec {
	if s.CPU == 0 {
		s.CPU = 1
	}
	if s.MemoryMB == 0 {
		s.MemoryMB = 256
	}
	if s.PidsMax == 0 {
		s.PidsMax = 256
	}
	return s
}

// ValidateRuntimeLimits rejects values that cannot be safely enforced.
func (s ComputeSpec) ValidateRuntimeLimits() error {
	if math.IsNaN(s.CPU) || math.IsInf(s.CPU, 0) || s.CPU < 0 || s.CPU > MaxRuntimeCPU || (s.CPU > 0 && s.CPU < 0.01) {
		return fmt.Errorf("runtime: cpu must be zero/default or between 0.01 and %d", MaxRuntimeCPU)
	}
	if s.MemoryMB < 0 || s.MemoryMB > MaxRuntimeMemoryMB {
		return fmt.Errorf("runtime: memoryMb must be between 0 and %d", MaxRuntimeMemoryMB)
	}
	if s.PidsMax < 0 || s.PidsMax > MaxRuntimePids {
		return fmt.Errorf("runtime: pidsMax must be between 0 and %d", MaxRuntimePids)
	}
	return nil
}
