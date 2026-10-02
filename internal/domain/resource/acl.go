// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import (
	"fmt"
	"net"
)

// KindACLPolicy is the resource kind for access-control policies.
const KindACLPolicy = "acl_policy"

// ACLFinalizer is attached by the agent so the iptables chain is torn down
// before the policy record is removed.
const ACLFinalizer = "infra.io/acl"

// ACLRule is one ingress filtering rule. An optional RateLimit turns an allow
// rule into a rate-limited (WAF-style) accept.
type ACLRule struct {
	// Action is "allow" or "deny".
	Action string `json:"action"`
	// Protocol is "tcp", "udp", "icmp" or "all" (default "all").
	Protocol string `json:"protocol,omitempty"`
	// Port is the destination port (tcp/udp only); zero means any.
	Port int `json:"port,omitempty"`
	// CIDR restricts the source address range; empty means any.
	CIDR string `json:"cidr,omitempty"`
	// RateLimit is an iptables limit expression, e.g. "10/second". Optional.
	RateLimit string `json:"rateLimit,omitempty"`
}

// ACLPolicySpec is the desired state of an access-control policy.
type ACLPolicySpec struct {
	Rules []ACLRule `json:"rules"`
}

var aclActions = map[string]bool{"allow": true, "deny": true}
var aclProtocols = map[string]bool{"": true, "all": true, "tcp": true, "udp": true, "icmp": true}

// Validate reports whether the policy is well-formed.
func (s ACLPolicySpec) Validate() error {
	if len(s.Rules) == 0 {
		return fmt.Errorf("acl_policy: at least one rule is required")
	}
	for i, r := range s.Rules {
		if !aclActions[r.Action] {
			return fmt.Errorf("acl_policy: rule %d: action must be allow or deny", i)
		}
		if !aclProtocols[r.Protocol] {
			return fmt.Errorf("acl_policy: rule %d: invalid protocol %q", i, r.Protocol)
		}
		if r.Port < 0 || r.Port > 65535 {
			return fmt.Errorf("acl_policy: rule %d: port out of range", i)
		}
		if r.Port != 0 && r.Protocol != "tcp" && r.Protocol != "udp" {
			return fmt.Errorf("acl_policy: rule %d: port requires tcp or udp", i)
		}
		if r.CIDR != "" {
			if _, _, err := net.ParseCIDR(r.CIDR); err != nil {
				return fmt.Errorf("acl_policy: rule %d: invalid cidr %q", i, r.CIDR)
			}
		}
	}
	return nil
}

// ACLPolicyStatus is the observed state of a policy.
type ACLPolicyStatus struct {
	StatusBase
	// Chain is the iptables chain backing the policy.
	Chain string `json:"chain,omitempty"`
	// AppliedRules is the number of rules currently applied.
	AppliedRules int `json:"appliedRules,omitempty"`
}

// ACLPolicy is an access-control policy resource.
type ACLPolicy = Resource[ACLPolicySpec, ACLPolicyStatus]
