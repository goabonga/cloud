// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import (
	"fmt"
	"net"
)

// KindSecurityGroup is the resource kind for security groups.
const KindSecurityGroup = "security_group"

// KindSecurityGroupRule is the resource kind for security-group rules.
const KindSecurityGroupRule = "security_group_rule"

// SecurityGroupFinalizer is attached by the agent so the iptables chain is
// flushed and deleted before the security-group record is removed.
const SecurityGroupFinalizer = "infra.io/security-group"

// SecurityGroupSpec is the desired state of a security group.
type SecurityGroupSpec struct {
	VPCID string `json:"vpcId"`
	Name  string `json:"name,omitempty"`
}

// Validate reports whether the spec is well-formed.
func (s SecurityGroupSpec) Validate() error {
	if s.VPCID == "" {
		return fmt.Errorf("security_group: vpcId is required")
	}
	return nil
}

// SecurityGroupStatus is the observed state of a security group.
type SecurityGroupStatus struct {
	StatusBase
	Chain string `json:"chain,omitempty"`
}

// SecurityGroup is a security-group resource.
type SecurityGroup = Resource[SecurityGroupSpec, SecurityGroupStatus]

// SecurityGroupRuleSpec is one rule attached to a security group.
type SecurityGroupRuleSpec struct {
	SecurityGroupID string `json:"securityGroupId"`
	// Direction is "ingress" or "egress".
	Direction string `json:"direction"`
	// Protocol is "tcp", "udp", "icmp" or "all".
	Protocol string `json:"protocol"`
	Port     int    `json:"port,omitempty"`
	CIDR     string `json:"cidr,omitempty"`
}

// Validate reports whether the rule is well-formed.
func (s SecurityGroupRuleSpec) Validate() error {
	if s.SecurityGroupID == "" {
		return fmt.Errorf("security_group_rule: securityGroupId is required")
	}
	if s.Direction != "ingress" && s.Direction != "egress" {
		return fmt.Errorf("security_group_rule: direction must be ingress or egress")
	}
	switch s.Protocol {
	case "tcp", "udp", "icmp", "all":
	default:
		return fmt.Errorf("security_group_rule: invalid protocol %q", s.Protocol)
	}
	if s.Port < 0 || s.Port > 65535 {
		return fmt.Errorf("security_group_rule: port out of range")
	}
	if s.CIDR != "" {
		if _, _, err := net.ParseCIDR(s.CIDR); err != nil {
			return fmt.Errorf("security_group_rule: invalid cidr %q", s.CIDR)
		}
	}
	return nil
}

// SecurityGroupRuleStatus is the observed state of a rule.
type SecurityGroupRuleStatus struct {
	StatusBase
}

// SecurityGroupRule is a security-group rule resource.
type SecurityGroupRule = Resource[SecurityGroupRuleSpec, SecurityGroupRuleStatus]
