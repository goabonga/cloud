// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package resource

import (
	"fmt"
	"strings"
)

// Layer-7 load-balancing kinds. A listener accepts connections on a port of a
// load balancer and routes them, by host and path (or by SNI for TLS
// passthrough), to target groups; a target group names its protocol and health
// check, and targets attach compute instances to it.
const (
	KindLBTargetGroup = "lb_target_group"
	KindLBTarget      = "lb_target"
	KindLBListener    = "lb_listener"
)

// Listener and target-group protocols.
const (
	LBProtocolHTTP  = "http"
	LBProtocolHTTPS = "https"
	LBProtocolTLS   = "tls"
	LBProtocolTCP   = "tcp"
)

// How an https or tls listener handles TLS: terminate it and speak plain HTTP
// to the targets, terminate it and open new TLS connections to the targets,
// or pass it through untouched, routing on the SNI.
const (
	TLSModeTerminate   = "terminate"
	TLSModeReencrypt   = "reencrypt"
	TLSModePassthrough = "passthrough"
)

// Target health, as reported by agents.
const (
	TargetHealthy   = "healthy"
	TargetUnhealthy = "unhealthy"
	TargetUnknown   = "unknown"
)

// Defaulter is implemented by spec types that fill server-side defaults.
type Defaulter[S any] interface {
	WithDefaults() S
}

// LBHealthCheck is how a target group probes its targets.
type LBHealthCheck struct {
	// Protocol is "http", "https" or "tcp"; the group's protocol by default.
	Protocol string `json:"protocol,omitempty"`
	// Path is the HTTP path probed; "/" by default, empty for tcp.
	Path string `json:"path,omitempty"`
	// Port is the port probed; 0 probes the traffic port.
	Port               int `json:"port,omitempty"`
	IntervalSeconds    int `json:"intervalSeconds,omitempty"`
	TimeoutSeconds     int `json:"timeoutSeconds,omitempty"`
	HealthyThreshold   int `json:"healthyThreshold,omitempty"`
	UnhealthyThreshold int `json:"unhealthyThreshold,omitempty"`
}

// LBTargetGroupSpec is the desired state of a pool of targets in a VPC.
type LBTargetGroupSpec struct {
	VPCID string `json:"vpcId"`
	// Protocol is how the load balancer talks to the targets: "http"
	// (default), "https" or "tcp".
	Protocol string `json:"protocol,omitempty"`
	// Port is the targets' port, unless a target names its own.
	Port int `json:"port"`
	// BackendCAID is the CA that verifies the targets' certificates with
	// https; empty trusts the platform's global CAs.
	BackendCAID string `json:"backendCaId,omitempty"`
	// ServerName is, with https, the name the targets' certificates are
	// verified against and asked for by SNI; empty uses the request's host.
	ServerName  string        `json:"serverName,omitempty"`
	HealthCheck LBHealthCheck `json:"healthCheck"`
}

// WithDefaults implements Defaulter.
func (s LBTargetGroupSpec) WithDefaults() LBTargetGroupSpec {
	if s.Protocol == "" {
		s.Protocol = LBProtocolHTTP
	}
	hc := &s.HealthCheck
	if hc.Protocol == "" {
		hc.Protocol = s.Protocol
	}
	if hc.Path == "" && hc.Protocol != LBProtocolTCP {
		hc.Path = "/"
	}
	if hc.IntervalSeconds == 0 {
		hc.IntervalSeconds = 5
	}
	if hc.TimeoutSeconds == 0 {
		hc.TimeoutSeconds = 2
	}
	if hc.HealthyThreshold == 0 {
		hc.HealthyThreshold = 2
	}
	if hc.UnhealthyThreshold == 0 {
		hc.UnhealthyThreshold = 3
	}
	return s
}

// Validate reports whether the spec, with its defaults, is well-formed.
func (s LBTargetGroupSpec) Validate() error {
	s = s.WithDefaults()
	if s.VPCID == "" {
		return fmt.Errorf("lb_target_group: vpcId is required")
	}
	switch s.Protocol {
	case LBProtocolHTTP, LBProtocolHTTPS, LBProtocolTCP:
	default:
		return fmt.Errorf("lb_target_group: protocol must be http, https or tcp")
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("lb_target_group: port out of range")
	}
	if s.BackendCAID != "" && s.Protocol != LBProtocolHTTPS {
		return fmt.Errorf("lb_target_group: backendCaId requires the https protocol")
	}
	if s.ServerName != "" && s.Protocol != LBProtocolHTTPS {
		return fmt.Errorf("lb_target_group: serverName requires the https protocol")
	}
	hc := s.HealthCheck
	switch hc.Protocol {
	case LBProtocolHTTP, LBProtocolHTTPS:
		if !strings.HasPrefix(hc.Path, "/") {
			return fmt.Errorf("lb_target_group: health check path must start with /")
		}
	case LBProtocolTCP:
		if hc.Path != "" {
			return fmt.Errorf("lb_target_group: a tcp health check takes no path")
		}
	default:
		return fmt.Errorf("lb_target_group: health check protocol must be http, https or tcp")
	}
	if hc.Port < 0 || hc.Port > 65535 {
		return fmt.Errorf("lb_target_group: health check port out of range")
	}
	if hc.IntervalSeconds < 1 || hc.TimeoutSeconds < 1 || hc.TimeoutSeconds >= hc.IntervalSeconds {
		return fmt.Errorf("lb_target_group: health check timeout must be at least 1s and shorter than the interval")
	}
	if hc.HealthyThreshold < 1 || hc.HealthyThreshold > 10 || hc.UnhealthyThreshold < 1 || hc.UnhealthyThreshold > 10 {
		return fmt.Errorf("lb_target_group: health check thresholds must be between 1 and 10")
	}
	return nil
}

// LBTargetHealth is one target's health as an agent reports it.
type LBTargetHealth struct {
	ComputeID string `json:"computeId"`
	Address   string `json:"address,omitempty"`
	Port      int    `json:"port,omitempty"`
	// Health is "healthy", "unhealthy" or "unknown".
	Health   string `json:"health"`
	NodeName string `json:"nodeName,omitempty"`
}

// LBTargetGroupStatus is the observed state of a target group. Targets are
// reported by agents.
type LBTargetGroupStatus struct {
	StatusBase
	Targets []LBTargetHealth `json:"targets,omitempty"`
}

// LBTargetGroup is a target-group resource.
type LBTargetGroup = Resource[LBTargetGroupSpec, LBTargetGroupStatus]

// LBTargetSpec attaches a compute instance to a target group.
type LBTargetSpec struct {
	TargetGroupID string `json:"targetGroupId"`
	ComputeID     string `json:"computeId"`
	// Port overrides the group's port; 0 keeps it.
	Port int `json:"port,omitempty"`
	// Weight is the target's share of the traffic, 1 to 1000; 1 by default.
	Weight int `json:"weight,omitempty"`
}

// WithDefaults implements Defaulter.
func (s LBTargetSpec) WithDefaults() LBTargetSpec {
	if s.Weight == 0 {
		s.Weight = 1
	}
	return s
}

// Validate reports whether the spec, with its defaults, is well-formed.
func (s LBTargetSpec) Validate() error {
	s = s.WithDefaults()
	if s.TargetGroupID == "" {
		return fmt.Errorf("lb_target: targetGroupId is required")
	}
	if s.ComputeID == "" {
		return fmt.Errorf("lb_target: computeId is required")
	}
	if s.Port < 0 || s.Port > 65535 {
		return fmt.Errorf("lb_target: port out of range")
	}
	if s.Weight < 1 || s.Weight > 1000 {
		return fmt.Errorf("lb_target: weight must be between 1 and 1000")
	}
	return nil
}

// LBTargetStatus is the observed state of a target.
type LBTargetStatus struct {
	StatusBase
}

// LBTarget is a target resource.
type LBTarget = Resource[LBTargetSpec, LBTargetStatus]

// LBListenerRule routes the requests matching its host and path prefix - or,
// on a tls listener, its SNI host - to a target group.
type LBListenerRule struct {
	Host          string `json:"host,omitempty"`
	PathPrefix    string `json:"pathPrefix,omitempty"`
	TargetGroupID string `json:"targetGroupId"`
}

// LBListenerSpec is the desired state of a listener on a load balancer.
type LBListenerSpec struct {
	LoadBalancerID string `json:"loadBalancerId"`
	Port           int    `json:"port"`
	// Protocol is "http", "https", "tls" or "tcp".
	Protocol string `json:"protocol"`
	// TLSMode is "terminate" (default) or "reencrypt" with https, and
	// "passthrough" with tls.
	TLSMode string `json:"tlsMode,omitempty"`
	// CertificateIDs are the ssl_certs an https listener serves, picked by SNI.
	CertificateIDs       []string         `json:"certificateIds,omitempty"`
	DefaultTargetGroupID string           `json:"defaultTargetGroupId"`
	Rules                []LBListenerRule `json:"rules,omitempty"`
}

// WithDefaults implements Defaulter.
func (s LBListenerSpec) WithDefaults() LBListenerSpec {
	if s.TLSMode == "" {
		switch s.Protocol {
		case LBProtocolHTTPS:
			s.TLSMode = TLSModeTerminate
		case LBProtocolTLS:
			s.TLSMode = TLSModePassthrough
		}
	}
	return s
}

// Validate reports whether the spec, with its defaults, is well-formed.
func (s LBListenerSpec) Validate() error {
	s = s.WithDefaults()
	if s.LoadBalancerID == "" {
		return fmt.Errorf("lb_listener: loadBalancerId is required")
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("lb_listener: port out of range")
	}
	if s.DefaultTargetGroupID == "" {
		return fmt.Errorf("lb_listener: defaultTargetGroupId is required")
	}
	for _, id := range s.CertificateIDs {
		if id == "" {
			return fmt.Errorf("lb_listener: certificateIds must not hold an empty id")
		}
	}
	switch s.Protocol {
	case LBProtocolHTTP:
		if s.TLSMode != "" || len(s.CertificateIDs) > 0 {
			return fmt.Errorf("lb_listener: an http listener takes no tlsMode nor certificates")
		}
	case LBProtocolHTTPS:
		if s.TLSMode != TLSModeTerminate && s.TLSMode != TLSModeReencrypt {
			return fmt.Errorf("lb_listener: an https listener's tlsMode must be terminate or reencrypt")
		}
		if len(s.CertificateIDs) == 0 {
			return fmt.Errorf("lb_listener: an https listener needs at least one certificate")
		}
	case LBProtocolTLS:
		if s.TLSMode != TLSModePassthrough {
			return fmt.Errorf("lb_listener: a tls listener's tlsMode must be passthrough")
		}
		if len(s.CertificateIDs) > 0 {
			return fmt.Errorf("lb_listener: a tls listener passes TLS through and takes no certificates")
		}
	case LBProtocolTCP:
		if s.TLSMode != "" || len(s.CertificateIDs) > 0 || len(s.Rules) > 0 {
			return fmt.Errorf("lb_listener: a tcp listener takes no tlsMode, certificates nor rules")
		}
	default:
		return fmt.Errorf("lb_listener: protocol must be http, https, tls or tcp")
	}
	for i, r := range s.Rules {
		if err := r.validate(s.Protocol); err != nil {
			return fmt.Errorf("lb_listener: rule %d: %w", i, err)
		}
	}
	return nil
}

func (r LBListenerRule) validate(protocol string) error {
	if r.TargetGroupID == "" {
		return fmt.Errorf("targetGroupId is required")
	}
	if r.Host == "" && r.PathPrefix == "" {
		return fmt.Errorf("a rule matches a host, a path prefix or both")
	}
	if strings.ContainsAny(r.Host, "/: \t") {
		return fmt.Errorf("host %q is not a host name", r.Host)
	}
	if r.PathPrefix != "" {
		if protocol == LBProtocolTLS {
			return fmt.Errorf("a tls listener routes on the SNI host only")
		}
		if !strings.HasPrefix(r.PathPrefix, "/") {
			return fmt.Errorf("pathPrefix must start with /")
		}
	}
	return nil
}

// LBListenerStatus is the observed state of a listener.
type LBListenerStatus struct {
	StatusBase
}

// LBListener is a listener resource.
type LBListener = Resource[LBListenerSpec, LBListenerStatus]
