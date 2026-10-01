// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import "fmt"

// KindFunction is the resource kind for FaaS functions.
const KindFunction = "function"

// FunctionFinalizer is attached by the function controller so a function's
// warm pool (its function instances and their backing compute) is torn down
// before the function record is removed.
const FunctionFinalizer = "infra.io/function"

// WarmPoolPolicy controls how many pre-started instances a function keeps and
// what happens when none are available on invoke. It is deliberately
// expressive rather than a single on/off switch, so different service tiers
// can map onto it later without a schema change: MinWarm > 0 with
// AllowColdStart false is an "always warm" tier; MinWarm 0 with
// AllowColdStart true is a "cold/cheap" tier; MinWarm > 0, MaxWarm > MinWarm
// and AllowColdStart true is an elastic-burst tier. Billing itself is out of
// scope here.
type WarmPoolPolicy struct {
	// MinWarm is the floor of pre-started instances kept running regardless
	// of idle time.
	MinWarm int `json:"minWarm,omitempty"`
	// MaxWarm caps concurrent warm instances; 0 means unbounded.
	MaxWarm int `json:"maxWarm,omitempty"`
	// IdleTTLSeconds is how long an instance above MinWarm may sit unused
	// before the pool evicts it; 0 evicts as soon as it is idle.
	IdleTTLSeconds int `json:"idleTtlSeconds,omitempty"`
	// AllowColdStart permits creating a fresh instance on invoke when the
	// pool has none available, bypassing MinWarm/MaxWarm.
	AllowColdStart bool `json:"allowColdStart,omitempty"`
}

// Validate reports whether the policy is well-formed.
func (p WarmPoolPolicy) Validate() error {
	if p.MinWarm < 0 {
		return fmt.Errorf("function: warmPool.minWarm must not be negative")
	}
	if p.MaxWarm < 0 {
		return fmt.Errorf("function: warmPool.maxWarm must not be negative")
	}
	if p.MaxWarm > 0 && p.MaxWarm < p.MinWarm {
		return fmt.Errorf("function: warmPool.maxWarm must be >= minWarm")
	}
	if p.IdleTTLSeconds < 0 {
		return fmt.Errorf("function: warmPool.idleTtlSeconds must not be negative")
	}
	return nil
}

// FunctionSpec is the desired state of a FaaS function: the compute shape run
// for each invocation, plus the warm-pool policy. It mirrors ComputeSpec's
// runtime fields (image, command, env, sizing, network attachment) without
// Disks, which belong to compute instances directly, not to the function
// shape.
type FunctionSpec struct {
	Name string `json:"name,omitempty"`
	// SubnetID attaches the function's instances to a subnet, same as a
	// compute instance.
	SubnetID        string `json:"subnetId"`
	SecurityGroupID string `json:"securityGroupId,omitempty"`
	// NodePoolID constrains placement to a node pool; empty schedules
	// anywhere, same as ComputeSpec.NodePoolID.
	NodePoolID string            `json:"nodePoolId,omitempty"`
	CPU        float64           `json:"cpu,omitempty"`
	MemoryMB   int               `json:"memoryMb,omitempty"`
	PidsMax    int               `json:"pidsMax,omitempty"`
	Image      string            `json:"image"`
	Command    string            `json:"command,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	// Port is the port the runtime listens on inside the instance; invoke
	// requests are forwarded to it.
	Port     int            `json:"port"`
	WarmPool WarmPoolPolicy `json:"warmPool,omitempty"`
}

// Validate reports whether the spec is well-formed.
func (s FunctionSpec) Validate() error {
	if s.SubnetID == "" {
		return fmt.Errorf("function: subnetId is required")
	}
	if s.Image == "" {
		return fmt.Errorf("function: image is required")
	}
	if s.CPU < 0 {
		return fmt.Errorf("function: cpu must not be negative")
	}
	if s.MemoryMB < 0 {
		return fmt.Errorf("function: memoryMb must not be negative")
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("function: port out of range")
	}
	return s.WarmPool.Validate()
}

// FunctionStatus is the observed state of a function. Phase reflects whether
// the function's shape is realizable (its subnet/VPC/security-group
// dependencies resolve, the same checks a real compute instance's own
// reconcile pass performs) independent of WarmCount: a function can be Ready
// with zero warm instances, the same way a deployment is available
// independent of any one replica's state.
type FunctionStatus struct {
	StatusBase
	// WarmCount is the number of function instances currently Warm and
	// backed by a Ready compute.
	WarmCount int `json:"warmCount"`
}

// Function is a FaaS function resource.
type Function = Resource[FunctionSpec, FunctionStatus]
