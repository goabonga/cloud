// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import (
	"fmt"
	"net"
)

// KindSubnet is the resource kind for subnets.
const KindSubnet = "subnet"

// SubnetFinalizer is attached by the agent so the gateway address is removed
// from the VPC bridge before the subnet record is deleted.
const SubnetFinalizer = "infra.io/subnet"

// SubnetSpec is the desired state of a subnet within a VPC.
type SubnetSpec struct {
	VPCID string `json:"vpcId"`
	CIDR  string `json:"cidr"`
	// Type is "public" or "private" (default "private").
	Type string `json:"type,omitempty"`
}

// Validate reports whether the spec is well-formed.
func (s SubnetSpec) Validate() error {
	if s.VPCID == "" {
		return fmt.Errorf("subnet: vpcId is required")
	}
	if _, _, err := net.ParseCIDR(s.CIDR); err != nil {
		return fmt.Errorf("subnet: invalid cidr %q: %w", s.CIDR, err)
	}
	switch s.Type {
	case "", "public", "private":
	default:
		return fmt.Errorf("subnet: type must be public or private")
	}
	return nil
}

// SubnetStatus is the observed state of a subnet.
type SubnetStatus struct {
	StatusBase
	Namespace string `json:"namespace,omitempty"`
	Gateway   string `json:"gateway,omitempty"`
}

// Subnet is a subnet resource.
type Subnet = Resource[SubnetSpec, SubnetStatus]
