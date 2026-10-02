// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import (
	"fmt"
	"net"
)

// Network resource kinds.
const (
	KindIPAddress = "ip_address"
	KindIGW       = "igw"
	KindRoute     = "route"
)

// IGWFinalizer is attached by the agent so the NAT rule is removed before the
// internet-gateway record is deleted.
const IGWFinalizer = "infra.io/igw"

// IPAddressSpec reserves an IP address, optionally bound to a subnet/compute.
type IPAddressSpec struct {
	// Type is "private" or "public".
	Type      string `json:"type,omitempty"`
	SubnetID  string `json:"subnetId,omitempty"`
	VPCID     string `json:"vpcId,omitempty"`
	ComputeID string `json:"computeId,omitempty"`
	// Address optionally requests a specific IP.
	Address string `json:"address,omitempty"`
}

// IPAddressStatus is the observed state of a reserved address.
type IPAddressStatus struct {
	StatusBase
	Address string `json:"address,omitempty"`
}

// IPAddress is a reserved-IP resource.
type IPAddress = Resource[IPAddressSpec, IPAddressStatus]

// IGWSpec is the desired state of an internet gateway.
type IGWSpec struct {
	VPCID string `json:"vpcId"`
	// EgressProxy, when enabled, filters the VPC's egress (see egress.go).
	EgressProxy *EgressProxySpec `json:"egressProxy,omitempty"`
}

// Validate reports whether the spec is well-formed.
func (s IGWSpec) Validate() error {
	if s.VPCID == "" {
		return fmt.Errorf("igw: vpcId is required")
	}
	if s.EgressProxy != nil {
		return s.EgressProxy.validate()
	}
	return nil
}

// IGWStatus is the observed state of an internet gateway.
type IGWStatus struct {
	StatusBase
	HostIface string `json:"hostIface,omitempty"`
	Bridge    string `json:"bridge,omitempty"`
	// EgressProxy is set while the egress proxy is enabled.
	EgressProxy *EgressProxyStatus `json:"egressProxy,omitempty"`
}

// IGW is an internet-gateway resource.
type IGW = Resource[IGWSpec, IGWStatus]

// RouteSpec is a static route within a VPC.
type RouteSpec struct {
	VPCID       string `json:"vpcId"`
	SubnetID    string `json:"subnetId,omitempty"`
	Destination string `json:"destination"`
	// Gateway is the target (e.g. an igw uid, "local", or a peer).
	Gateway string `json:"gateway"`
}

// Validate reports whether the spec is well-formed.
func (s RouteSpec) Validate() error {
	if s.VPCID == "" {
		return fmt.Errorf("route: vpcId is required")
	}
	if _, _, err := net.ParseCIDR(s.Destination); err != nil {
		return fmt.Errorf("route: invalid destination %q: %w", s.Destination, err)
	}
	if s.Gateway == "" {
		return fmt.Errorf("route: gateway is required")
	}
	return nil
}

// RouteStatus is the observed state of a route.
type RouteStatus struct {
	StatusBase
}

// Route is a static-route resource.
type Route = Resource[RouteSpec, RouteStatus]
