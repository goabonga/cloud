// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import (
	"fmt"
	"net"
	"strings"
)

// EgressProxySpec turns an internet gateway's egress into a filtered one:
// the VPC's HTTP and HTTPS traffic to public addresses goes through a proxy,
// transparently or explicitly, which lets out only the allowed domains, and
// every other new connection out of the gateway is refused unless its
// destination is an allowed address.
type EgressProxySpec struct {
	Enabled bool `json:"enabled"`
	// AllowedDomains are the names the proxy lets through: "example.com" for
	// that name alone, "*.example.com" for its subdomains.
	AllowedDomains []string `json:"allowedDomains,omitempty"`
	// AllowedAddresses are destinations reachable directly, past the proxy,
	// and through it when a request names an address rather than a domain.
	AllowedAddresses []EgressAddress `json:"allowedAddresses,omitempty"`
}

// EgressAddress is a destination allowed out of the gateway.
type EgressAddress struct {
	// CIDR is an address or a block, e.g. "203.0.113.7" or "198.51.100.0/24".
	CIDR string `json:"cidr"`
	// Protocol is "tcp", "udp" or "any" (default).
	Protocol string `json:"protocol,omitempty"`
	// Port restricts tcp or udp to one port; 0 allows them all.
	Port int `json:"port,omitempty"`
}

// EgressProxyStatus is what an internet gateway's egress proxy reports.
type EgressProxyStatus struct {
	// Address is where instances reach the proxy explicitly, on port 3128,
	// and where their HTTP and HTTPS traffic is redirected: the VPC's second
	// address.
	Address string `json:"address,omitempty"`
}

// validate reports whether the egress proxy spec is well-formed.
func (s *EgressProxySpec) validate() error {
	for _, d := range s.AllowedDomains {
		if err := validDomainPattern(d); err != nil {
			return fmt.Errorf("igw: egressProxy.allowedDomains: %w", err)
		}
	}
	for i, a := range s.AllowedAddresses {
		if _, err := a.Network(); err != nil {
			return fmt.Errorf("igw: egressProxy.allowedAddresses[%d]: %w", i, err)
		}
		switch a.Protocol {
		case "", "any":
			if a.Port != 0 {
				return fmt.Errorf("igw: egressProxy.allowedAddresses[%d]: a port needs protocol tcp or udp", i)
			}
		case "tcp", "udp":
		default:
			return fmt.Errorf("igw: egressProxy.allowedAddresses[%d]: protocol must be tcp, udp or any", i)
		}
		if a.Port < 0 || a.Port > 65535 {
			return fmt.Errorf("igw: egressProxy.allowedAddresses[%d]: port out of range", i)
		}
	}
	return nil
}

// Network is the address's CIDR, a lone address taken as a /32 or /128.
func (a EgressAddress) Network() (*net.IPNet, error) {
	if ip := net.ParseIP(a.CIDR); ip != nil {
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, nil
	}
	_, n, err := net.ParseCIDR(a.CIDR)
	if err != nil {
		return nil, fmt.Errorf("%q is neither an address nor a CIDR", a.CIDR)
	}
	return n, nil
}

// validDomainPattern accepts a lowercase DNS name, optionally behind "*.".
func validDomainPattern(p string) error {
	name := strings.TrimPrefix(p, "*.")
	if name == "" || len(name) > 253 || name != strings.ToLower(name) || strings.HasSuffix(name, ".") {
		return fmt.Errorf("%q is not a lowercase domain such as example.com or *.example.com", p)
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("%q is not a valid domain", p)
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return fmt.Errorf("%q is not a valid domain", p)
			}
		}
	}
	return nil
}
