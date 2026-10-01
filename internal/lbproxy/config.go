// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package lbproxy is the data plane of the load balancers: a layer-7 HTTP(S)
// reverse proxy, a TLS passthrough router and a TCP splicer, driven by a JSON
// configuration file it reloads on change and reporting the state of its
// listeners and targets to a JSON status file. cmd/lb wraps it as infra-lb.
package lbproxy

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
)

// Listener protocols.
const (
	ProtocolHTTP  = "http"
	ProtocolHTTPS = "https"
	ProtocolTLS   = "tls"
	ProtocolTCP   = "tcp"
)

// TLS modes of a listener.
const (
	TLSModeTerminate   = "terminate"
	TLSModeReencrypt   = "reencrypt"
	TLSModePassthrough = "passthrough"
)

// Health states of a target.
const (
	HealthUnknown   = "unknown"
	HealthHealthy   = "healthy"
	HealthUnhealthy = "unhealthy"
)

// Config is the whole data plane: what it listens on and where it sends.
type Config struct {
	Listeners    []Listener    `json:"listeners"`
	TargetGroups []TargetGroup `json:"targetGroups"`
}

// Listener accepts connections on Address:Port.
type Listener struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	// TLSMode is "terminate" or "reencrypt" for https (default terminate),
	// "passthrough" for tls (its only mode), empty otherwise.
	TLSMode string `json:"tlsMode,omitempty"`
	// Certificates are served by an https listener, chosen by SNI against
	// each leaf's DNS names; the first is the default.
	Certificates       []Certificate `json:"certificates,omitempty"`
	DefaultTargetGroup string        `json:"defaultTargetGroup"`
	Rules              []Rule        `json:"rules,omitempty"`
}

// Certificate is a PEM chain, leaf first, and its PEM private key.
type Certificate struct {
	CertPEM string `json:"certPem"`
	KeyPEM  string `json:"keyPem"`
}

// Rule sends what matches Host and PathPrefix to TargetGroup. Host matches
// exactly or, as "*.example.com", any name below the suffix; a tls listener
// matches it against the SNI and has no path.
type Rule struct {
	Host        string `json:"host,omitempty"`
	PathPrefix  string `json:"pathPrefix,omitempty"`
	TargetGroup string `json:"targetGroup"`
}

// TargetGroup is a set of backends sharing a protocol and a health check.
type TargetGroup struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	// BackendCAPEM verifies https backends; empty uses the system roots.
	BackendCAPEM string `json:"backendCaPem,omitempty"`
	// ServerName is the name https backends are verified against and sent
	// as SNI; empty uses the request's Host, without its port.
	ServerName  string      `json:"serverName,omitempty"`
	HealthCheck HealthCheck `json:"healthCheck"`
	Targets     []Target    `json:"targets"`
}

// HealthCheck probes every target of a group.
type HealthCheck struct {
	// Protocol is "http", "https" or "tcp"; empty uses the group's.
	Protocol string `json:"protocol,omitempty"`
	// Path is requested by http(s) checks, "/" by default.
	Path string `json:"path,omitempty"`
	// Port is probed instead of the target's port when set.
	Port               int `json:"port,omitempty"`
	IntervalSeconds    int `json:"intervalSeconds,omitempty"`
	TimeoutSeconds     int `json:"timeoutSeconds,omitempty"`
	HealthyThreshold   int `json:"healthyThreshold,omitempty"`
	UnhealthyThreshold int `json:"unhealthyThreshold,omitempty"`
}

// Target is one backend.
type Target struct {
	ID      string `json:"id,omitempty"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	Weight  int    `json:"weight,omitempty"`
}

// ParseConfig decodes, defaults and validates a configuration.
func ParseConfig(raw []byte) (*Config, error) {
	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("lbproxy: decode config: %w", err)
	}
	cfg.setDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) setDefaults() {
	for i := range c.Listeners {
		l := &c.Listeners[i]
		l.Protocol = strings.ToLower(l.Protocol)
		if l.TLSMode == "" {
			switch l.Protocol {
			case ProtocolHTTPS:
				l.TLSMode = TLSModeTerminate
			case ProtocolTLS:
				l.TLSMode = TLSModePassthrough
			}
		}
	}
	for i := range c.TargetGroups {
		g := &c.TargetGroups[i]
		g.Protocol = strings.ToLower(g.Protocol)
		hc := &g.HealthCheck
		if hc.Protocol == "" {
			hc.Protocol = g.Protocol
		}
		if hc.Path == "" && hc.Protocol != ProtocolTCP {
			hc.Path = "/"
		}
		if hc.IntervalSeconds <= 0 {
			hc.IntervalSeconds = 5
		}
		if hc.TimeoutSeconds <= 0 {
			hc.TimeoutSeconds = 2
		}
		if hc.HealthyThreshold <= 0 {
			hc.HealthyThreshold = 2
		}
		if hc.UnhealthyThreshold <= 0 {
			hc.UnhealthyThreshold = 3
		}
		for j := range g.Targets {
			t := &g.Targets[j]
			if t.Weight <= 0 {
				t.Weight = 1
			}
			if t.ID == "" {
				t.ID = net.JoinHostPort(t.Address, fmt.Sprint(t.Port))
			}
		}
	}
}

// Validate reports the first inconsistency of a defaulted configuration.
func (c *Config) Validate() error {
	groups := map[string]*TargetGroup{}
	for i := range c.TargetGroups {
		g := &c.TargetGroups[i]
		if err := g.validate(); err != nil {
			return err
		}
		if groups[g.Name] != nil {
			return fmt.Errorf("lbproxy: target group %q declared twice", g.Name)
		}
		groups[g.Name] = g
	}
	names := map[string]bool{}
	for i := range c.Listeners {
		l := &c.Listeners[i]
		if err := l.validate(groups); err != nil {
			return err
		}
		if names[l.Name] {
			return fmt.Errorf("lbproxy: listener %q declared twice", l.Name)
		}
		names[l.Name] = true
	}
	return nil
}

func (g *TargetGroup) validate() error {
	if g.Name == "" {
		return errors.New("lbproxy: a target group needs a name")
	}
	switch g.Protocol {
	case ProtocolHTTP, ProtocolHTTPS, ProtocolTCP:
	default:
		return fmt.Errorf("lbproxy: target group %q: protocol %q is not http, https or tcp", g.Name, g.Protocol)
	}
	if g.BackendCAPEM != "" {
		if g.Protocol != ProtocolHTTPS {
			return fmt.Errorf("lbproxy: target group %q: backendCaPem needs protocol https", g.Name)
		}
		if !x509.NewCertPool().AppendCertsFromPEM([]byte(g.BackendCAPEM)) {
			return fmt.Errorf("lbproxy: target group %q: backendCaPem holds no certificate", g.Name)
		}
	}
	hc := g.HealthCheck
	switch hc.Protocol {
	case ProtocolHTTP, ProtocolHTTPS, ProtocolTCP:
	default:
		return fmt.Errorf("lbproxy: target group %q: health check protocol %q is not http, https or tcp", g.Name, hc.Protocol)
	}
	if hc.Protocol != ProtocolTCP && !strings.HasPrefix(hc.Path, "/") {
		return fmt.Errorf("lbproxy: target group %q: health check path %q must start with /", g.Name, hc.Path)
	}
	if hc.Port < 0 || hc.Port > 65535 {
		return fmt.Errorf("lbproxy: target group %q: health check port %d is out of range", g.Name, hc.Port)
	}
	if hc.TimeoutSeconds > hc.IntervalSeconds {
		return fmt.Errorf("lbproxy: target group %q: health check timeout exceeds its interval", g.Name)
	}
	ids := map[string]bool{}
	for _, t := range g.Targets {
		if net.ParseIP(t.Address) == nil {
			return fmt.Errorf("lbproxy: target group %q: target address %q is not an IP", g.Name, t.Address)
		}
		if t.Port < 1 || t.Port > 65535 {
			return fmt.Errorf("lbproxy: target group %q: target port %d is out of range", g.Name, t.Port)
		}
		if t.Weight > 1000 {
			return fmt.Errorf("lbproxy: target group %q: target weight %d exceeds 1000", g.Name, t.Weight)
		}
		if ids[t.ID] {
			return fmt.Errorf("lbproxy: target group %q: target %q declared twice", g.Name, t.ID)
		}
		ids[t.ID] = true
	}
	return nil
}

func (l *Listener) validate(groups map[string]*TargetGroup) error {
	if l.Name == "" {
		return errors.New("lbproxy: a listener needs a name")
	}
	if l.Address != "" && net.ParseIP(l.Address) == nil {
		return fmt.Errorf("lbproxy: listener %q: address %q is not an IP", l.Name, l.Address)
	}
	if l.Port < 1 || l.Port > 65535 {
		return fmt.Errorf("lbproxy: listener %q: port %d is out of range", l.Name, l.Port)
	}
	// The group protocols each listener protocol and mode can send to.
	var accepts []string
	switch {
	case l.Protocol == ProtocolHTTP && l.TLSMode == "":
		accepts = []string{ProtocolHTTP}
	case l.Protocol == ProtocolHTTPS && l.TLSMode == TLSModeTerminate:
		accepts = []string{ProtocolHTTP}
	case l.Protocol == ProtocolHTTPS && l.TLSMode == TLSModeReencrypt:
		accepts = []string{ProtocolHTTPS}
	case l.Protocol == ProtocolTLS && l.TLSMode == TLSModePassthrough:
		accepts = []string{ProtocolTCP, ProtocolHTTPS}
	case l.Protocol == ProtocolTCP && l.TLSMode == "":
		accepts = []string{ProtocolTCP}
	default:
		return fmt.Errorf("lbproxy: listener %q: protocol %q with tls mode %q is not supported", l.Name, l.Protocol, l.TLSMode)
	}
	if l.Protocol == ProtocolHTTPS {
		if len(l.Certificates) == 0 {
			return fmt.Errorf("lbproxy: listener %q: https needs a certificate", l.Name)
		}
		for i, c := range l.Certificates {
			if _, err := tls.X509KeyPair([]byte(c.CertPEM), []byte(c.KeyPEM)); err != nil {
				return fmt.Errorf("lbproxy: listener %q: certificate %d: %w", l.Name, i, err)
			}
		}
	} else if len(l.Certificates) > 0 {
		return fmt.Errorf("lbproxy: listener %q: only https serves certificates", l.Name)
	}
	check := func(name string) error {
		g := groups[name]
		if g == nil {
			return fmt.Errorf("lbproxy: listener %q: target group %q is not declared", l.Name, name)
		}
		for _, p := range accepts {
			if g.Protocol == p {
				return nil
			}
		}
		return fmt.Errorf("lbproxy: listener %q: target group %q speaks %s, not %s", l.Name, name, g.Protocol, strings.Join(accepts, " or "))
	}
	if err := check(l.DefaultTargetGroup); err != nil {
		return err
	}
	if l.Protocol == ProtocolTCP && len(l.Rules) > 0 {
		return fmt.Errorf("lbproxy: listener %q: tcp has no rules", l.Name)
	}
	for _, r := range l.Rules {
		if r.Host == "" && r.PathPrefix == "" {
			return fmt.Errorf("lbproxy: listener %q: a rule needs a host or a path prefix", l.Name)
		}
		if r.PathPrefix != "" && !strings.HasPrefix(r.PathPrefix, "/") {
			return fmt.Errorf("lbproxy: listener %q: path prefix %q must start with /", l.Name, r.PathPrefix)
		}
		if l.Protocol == ProtocolTLS && r.PathPrefix != "" {
			return fmt.Errorf("lbproxy: listener %q: tls rules match the SNI only, not a path", l.Name)
		}
		if err := check(r.TargetGroup); err != nil {
			return err
		}
	}
	return nil
}
