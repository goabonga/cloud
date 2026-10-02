// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "fmt"

// KindPeering is the resource kind for VPC peerings.
const KindPeering = "peering"

// PeeringFinalizer is attached by the agent so the veth link between the two VPC
// bridges is removed before the peering record is deleted.
const PeeringFinalizer = "infra.io/peering"

// PeeringSpec connects two VPCs so workloads can route between them.
type PeeringSpec struct {
	VPC1ID string `json:"vpc1Id"`
	VPC2ID string `json:"vpc2Id"`
}

// Validate reports whether the spec is well-formed.
func (s PeeringSpec) Validate() error {
	if s.VPC1ID == "" || s.VPC2ID == "" {
		return fmt.Errorf("peering: vpc1Id and vpc2Id are required")
	}
	if s.VPC1ID == s.VPC2ID {
		return fmt.Errorf("peering: cannot peer a VPC with itself")
	}
	return nil
}

// PeeringStatus is the observed state of a peering.
type PeeringStatus struct {
	StatusBase
	// Veth1 and Veth2 are the veth interfaces linking the two VPC bridges.
	Veth1 string `json:"veth1,omitempty"`
	Veth2 string `json:"veth2,omitempty"`
}

// Peering is a VPC-peering resource.
type Peering = Resource[PeeringSpec, PeeringStatus]
