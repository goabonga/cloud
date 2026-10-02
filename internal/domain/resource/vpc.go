// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import (
	"fmt"
	"net"
)

// KindVPC is the resource kind for virtual private clouds.
const KindVPC = "vpc"

// VPCFinalizer is the finalizer the agent attaches to a VPC so the Linux bridge
// is torn down before the resource record is removed.
const VPCFinalizer = "infra.io/vpc"

// VPCSpec is the desired state of a VPC.
type VPCSpec struct {
	// CIDR is the address range of the VPC in CIDR notation (e.g. 10.0.0.0/16).
	CIDR string `json:"cidr"`
}

// Validate reports whether the spec is well-formed.
func (s VPCSpec) Validate() error {
	if s.CIDR == "" {
		return fmt.Errorf("vpc: cidr is required")
	}
	if _, _, err := net.ParseCIDR(s.CIDR); err != nil {
		return fmt.Errorf("vpc: invalid cidr %q: %w", s.CIDR, err)
	}
	return nil
}

// VPCStatus is the observed state of a VPC.
type VPCStatus struct {
	StatusBase
	// BridgeName is the Linux bridge backing the VPC, set by the agent.
	BridgeName string `json:"bridgeName,omitempty"`
}

// VPC is a virtual private cloud resource.
type VPC = Resource[VPCSpec, VPCStatus]
