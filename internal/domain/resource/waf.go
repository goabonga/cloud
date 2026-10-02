// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "fmt"

// WAF resource kinds.
const (
	KindWAFPolicy = "waf_policy"
	KindWAFRule   = "waf_rule"
)

// WAFPolicyFinalizer is attached by the agent so the iptables chain and its
// attachment to the target are removed before the policy record is deleted.
const WAFPolicyFinalizer = "infra.io/waf-policy"

// WAFPolicySpec is the desired state of a web-application-firewall policy
// attached to an internet gateway, subnet or compute instance.
type WAFPolicySpec struct {
	Name string `json:"name,omitempty"`
	// TargetType is "igw", "subnet" or "compute".
	TargetType string `json:"targetType"`
	TargetID   string `json:"targetId"`
	LogEnabled bool   `json:"logEnabled,omitempty"`
}

// Validate reports whether the spec is well-formed.
func (s WAFPolicySpec) Validate() error {
	switch s.TargetType {
	case "igw", "subnet", "compute":
	default:
		return fmt.Errorf("waf_policy: targetType must be igw, subnet or compute")
	}
	if s.TargetID == "" {
		return fmt.Errorf("waf_policy: targetId is required")
	}
	return nil
}

// WAFPolicyStatus is the observed state of a WAF policy.
type WAFPolicyStatus struct {
	StatusBase
	Chain string `json:"chain,omitempty"`
}

// WAFPolicy is a WAF policy resource.
type WAFPolicy = Resource[WAFPolicySpec, WAFPolicyStatus]

// WAFRuleSpec is one rule within a WAF policy.
type WAFRuleSpec struct {
	PolicyID   string `json:"policyId"`
	Priority   int    `json:"priority,omitempty"`
	MatchType  string `json:"matchType"`
	MatchValue string `json:"matchValue,omitempty"`
	// Action is "block", "allow", "log" or "ratelimit".
	Action     string `json:"action"`
	RateLimit  int    `json:"rateLimit,omitempty"`
	RateWindow int    `json:"rateWindow,omitempty"`
}

// Validate reports whether the spec is well-formed.
func (s WAFRuleSpec) Validate() error {
	if s.PolicyID == "" {
		return fmt.Errorf("waf_rule: policyId is required")
	}
	if s.MatchType == "" {
		return fmt.Errorf("waf_rule: matchType is required")
	}
	switch s.Action {
	case "block", "allow", "log", "ratelimit":
	default:
		return fmt.Errorf("waf_rule: action must be block, allow, log or ratelimit")
	}
	if s.Action == "ratelimit" && s.RateLimit <= 0 {
		return fmt.Errorf("waf_rule: ratelimit action requires a positive rateLimit")
	}
	return nil
}

// WAFRuleStatus is the observed state of a WAF rule.
type WAFRuleStatus struct {
	StatusBase
}

// WAFRule is a WAF rule resource.
type WAFRule = Resource[WAFRuleSpec, WAFRuleStatus]
