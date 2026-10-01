// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "fmt"

// Load-balancing resource kinds.
const (
	KindLoadBalancer = "load_balancer"
	KindLBBackend    = "lb_backend"
)

// LoadBalancerFinalizer is attached by the agent so the IPVS virtual service and
// its VIP are removed before the load-balancer record is deleted.
const LoadBalancerFinalizer = "infra.io/load-balancer"

// LoadBalancerSpec is the desired state of a layer-4 load balancer fronting a
// pool of compute backends within a VPC.
type LoadBalancerSpec struct {
	Name    string `json:"name,omitempty"`
	VPCID   string `json:"vpcId"`
	Address string `json:"address,omitempty"`
	// Port is the layer-4 virtual service's; 0 gives the load balancer none,
	// for its lb_listeners alone to serve its addresses.
	Port int `json:"port,omitempty"`
	// Protocol is "tcp" or "udp" (default "tcp").
	Protocol string `json:"protocol,omitempty"`
	// Algorithm is "round_robin", "least_conn" or "source" (default "round_robin").
	Algorithm string `json:"algorithm,omitempty"`
	// PublicIPID optionally names a public ip_address the load balancer is
	// also reachable on, from outside the VPC, through the edges.
	PublicIPID string `json:"publicIpId,omitempty"`
}

// Validate reports whether the spec is well-formed.
func (s LoadBalancerSpec) Validate() error {
	if s.VPCID == "" {
		return fmt.Errorf("load_balancer: vpcId is required")
	}
	if s.Port < 0 || s.Port > 65535 {
		return fmt.Errorf("load_balancer: port out of range")
	}
	switch s.Protocol {
	case "", "tcp", "udp":
	default:
		return fmt.Errorf("load_balancer: protocol must be tcp or udp")
	}
	switch s.Algorithm {
	case "", "round_robin", "least_conn", "source":
	default:
		return fmt.Errorf("load_balancer: invalid algorithm %q", s.Algorithm)
	}
	return nil
}

// LoadBalancerStatus is the observed state of a load balancer.
type LoadBalancerStatus struct {
	StatusBase
	Address   string `json:"address,omitempty"`
	ServiceID string `json:"serviceId,omitempty"`
	// PublicAddress is the public address the edges serve the load balancer
	// on, once its public ip_address is resolved.
	PublicAddress string `json:"publicAddress,omitempty"`
}

// LoadBalancer is a load-balancer resource.
type LoadBalancer = Resource[LoadBalancerSpec, LoadBalancerStatus]

// LBBackendSpec attaches a compute instance to a load balancer as a real server.
type LBBackendSpec struct {
	LBID      string `json:"lbId"`
	ComputeID string `json:"computeId"`
	Port      int    `json:"port"`
	Weight    int    `json:"weight,omitempty"`
}

// Validate reports whether the spec is well-formed.
func (s LBBackendSpec) Validate() error {
	if s.LBID == "" {
		return fmt.Errorf("lb_backend: lbId is required")
	}
	if s.ComputeID == "" {
		return fmt.Errorf("lb_backend: computeId is required")
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("lb_backend: port out of range")
	}
	if s.Weight < 0 {
		return fmt.Errorf("lb_backend: weight must not be negative")
	}
	return nil
}

// LBBackendStatus is the observed state of a load-balancer backend.
type LBBackendStatus struct {
	StatusBase
	RealServerIP string `json:"realServerIp,omitempty"`
}

// LBBackend is a load-balancer backend resource.
type LBBackend = Resource[LBBackendSpec, LBBackendStatus]
