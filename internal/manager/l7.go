// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/lbproxy"
	"github.com/goabonga/infrastructure/internal/registry"
)

// A load balancer's listeners are served by infra-lb, the data plane, running
// in the VPC's load-balancer namespace on every host (see lbns.go), next to
// the IPVS services of the load balancers' ports. The listener pass turns the
// listeners, target groups and targets of each VPC into infra-lb's
// configuration - each listener on its load balancer's VIP and, on an edge,
// its public address, the certificates rendered from the store - writes it,
// starts infra-lb@<namespace> or has it reload, and feeds what infra-lb
// reports back: whether each listener holds its port, and each target's
// health as seen from this host.

// LBListenerRegistry is the typed store of listeners.
type LBListenerRegistry = registry.Registry[resource.LBListenerSpec, resource.LBListenerStatus]

// LBTargetGroupRegistry is the typed store of target groups.
type LBTargetGroupRegistry = registry.Registry[resource.LBTargetGroupSpec, resource.LBTargetGroupStatus]

// LBTargetRegistry is the typed store of targets.
type LBTargetRegistry = registry.Registry[resource.LBTargetSpec, resource.LBTargetStatus]

// TLSSource renders what the listeners need of the platform's PKI.
type TLSSource interface {
	CertificateSource
	// CACertificate returns a CA's certificate, PEM.
	CACertificate(caID string) ([]byte, error)
}

// DataPlane runs infra-lb in the load-balancer namespaces, each named as
// lbNamespaceName names it.
type DataPlane interface {
	// Apply makes infra-lb serve cfg in the namespace: it writes the
	// configuration, starts infra-lb or has it reload when it changed.
	Apply(ctx context.Context, namespace string, cfg *lbproxy.Config) error
	// Stop stops infra-lb in the namespace and forgets its configuration.
	// Stopping one not running is not an error.
	Stop(ctx context.Context, namespace string) error
	// Status returns what infra-lb last reported in the namespace, and
	// whether it reported anything.
	Status(namespace string) (lbproxy.Status, bool)
	// Serving lists the namespaces that have a configuration.
	Serving() []string
}

// ListenerSource adds listeners and target groups of its own to a VPC's
// infra-lb, such as those serving an egress proxy's address (see egress.go).
type ListenerSource interface {
	ExtraConfig(vpcID string) ([]lbproxy.Listener, []lbproxy.TargetGroup)
}

// ListenerReconciler is the listener pass.
type ListenerReconciler struct {
	listeners *LBListenerRegistry
	groups    *LBTargetGroupRegistry
	targets   *LBTargetRegistry
	lbs       *LoadBalancerRegistry
	computes  *ComputeRegistry
	vpcs      *VPCRegistry
	plane     DataPlane
	tls       TLSSource
	nodeName  string
	edge      bool
	sources   []ListenerSource
}

// NewListenerReconciler returns the listener pass for the node named
// nodeName. tls may be nil on an agent without the KMS key: https listeners
// then fail.
func NewListenerReconciler(listeners *LBListenerRegistry, groups *LBTargetGroupRegistry, targets *LBTargetRegistry,
	lbs *LoadBalancerRegistry, computes *ComputeRegistry, vpcs *VPCRegistry, plane DataPlane, tls TLSSource, nodeName string) *ListenerReconciler {
	return &ListenerReconciler{listeners: listeners, groups: groups, targets: targets, lbs: lbs, computes: computes,
		vpcs: vpcs, plane: plane, tls: tls, nodeName: nodeName}
}

// AsEdge serves each listener on its load balancer's public address too, as
// the load-balancer pass does on an edge.
func (r *ListenerReconciler) AsEdge() *ListenerReconciler {
	r.edge = true
	return r
}

// WithSources adds the listeners and target groups of sources to each VPC's
// configuration. They run before this pass.
func (r *ListenerReconciler) WithSources(sources ...ListenerSource) *ListenerReconciler {
	r.sources = append(r.sources, sources...)
	return r
}

// Name identifies the reconcile pass.
func (r *ListenerReconciler) Name() string { return resource.KindLBListener }

// l7Store is one pass's view of the store.
type l7Store struct {
	listeners []resource.LBListener
	groups    map[string]*resource.LBTargetGroup
	targets   []resource.LBTarget
	lbs       map[string]*resource.LoadBalancer
	computes  map[string]*resource.Compute
}

// ReconcileAll configures infra-lb in every VPC's namespace.
func (r *ListenerReconciler) ReconcileAll(ctx context.Context) error {
	st, err := r.load()
	if err != nil {
		return err
	}
	vpcs, err := r.vpcs.List()
	if err != nil {
		return fmt.Errorf("manager: list vpcs: %w", err)
	}
	var errs []error
	configured := map[string]bool{}
	for i := range vpcs {
		v := &vpcs[i]
		if v.Metadata.IsDeleting() || v.Status.BridgeName == "" {
			continue
		}
		cfg, problems := r.config(st, v.Metadata.UID)
		for _, src := range r.sources {
			ls, gs := src.ExtraConfig(v.Metadata.UID)
			cfg.Listeners = append(cfg.Listeners, ls...)
			cfg.TargetGroups = append(cfg.TargetGroups, gs...)
		}
		if len(cfg.Listeners) == 0 {
			continue
		}
		ns := lbNamespaceName(v.Metadata.UID)
		configured[ns] = true
		if err := cfg.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("vpc %s listeners: %w", v.Metadata.UID, err))
			continue
		}
		if err := r.plane.Apply(ctx, ns, cfg); err != nil {
			errs = append(errs, fmt.Errorf("vpc %s listeners: %w", v.Metadata.UID, err))
			continue
		}
		status, _ := r.plane.Status(ns)
		r.report(st, v.Metadata.UID, cfg, status, problems)
	}
	for _, ns := range r.plane.Serving() {
		if !configured[ns] {
			if err := r.plane.Stop(ctx, ns); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (r *ListenerReconciler) load() (*l7Store, error) {
	listeners, err := r.listeners.List()
	if err != nil {
		return nil, fmt.Errorf("manager: list listeners: %w", err)
	}
	groups, err := r.groups.List()
	if err != nil {
		return nil, fmt.Errorf("manager: list target groups: %w", err)
	}
	targets, err := r.targets.List()
	if err != nil {
		return nil, fmt.Errorf("manager: list targets: %w", err)
	}
	lbs, err := r.lbs.List()
	if err != nil {
		return nil, fmt.Errorf("manager: list load balancers: %w", err)
	}
	computes, err := r.computes.List()
	if err != nil {
		return nil, fmt.Errorf("manager: list computes: %w", err)
	}
	st := &l7Store{
		listeners: listeners, targets: targets,
		groups: map[string]*resource.LBTargetGroup{}, lbs: map[string]*resource.LoadBalancer{}, computes: map[string]*resource.Compute{},
	}
	for i := range groups {
		if !groups[i].Metadata.IsDeleting() {
			st.groups[groups[i].Metadata.UID] = &groups[i]
		}
	}
	for i := range lbs {
		if !lbs[i].Metadata.IsDeleting() {
			st.lbs[lbs[i].Metadata.UID] = &lbs[i]
		}
	}
	for i := range computes {
		st.computes[computes[i].Metadata.UID] = &computes[i]
	}
	return st, nil
}

// listenerName names a listener's entry for one of its addresses.
func listenerName(uid, addr string) string { return uid + "@" + addr }

// config builds the VPC's infra-lb configuration. A listener that cannot be
// served yet - its load balancer has no address, a certificate is not
// issued - is left out, with the reason in problems.
func (r *ListenerReconciler) config(st *l7Store, vpcID string) (*lbproxy.Config, map[string]error) {
	cfg := &lbproxy.Config{Listeners: []lbproxy.Listener{}, TargetGroups: []lbproxy.TargetGroup{}}
	problems := map[string]error{}
	usedGroups := map[string]bool{}
	for i := range st.listeners {
		l := &st.listeners[i]
		lb, ok := st.lbs[l.Spec.LoadBalancerID]
		if l.Metadata.IsDeleting() || !ok || lb.Spec.VPCID != vpcID {
			continue
		}
		spec := l.Spec.WithDefaults()
		entries, err := r.listenerEntries(st, l.Metadata.UID, spec, lb)
		if err != nil {
			problems[l.Metadata.UID] = err
			continue
		}
		cfg.Listeners = append(cfg.Listeners, entries...)
		usedGroups[spec.DefaultTargetGroupID] = true
		for _, rule := range spec.Rules {
			usedGroups[rule.TargetGroupID] = true
		}
	}
	for id := range usedGroups {
		g, err := r.targetGroup(st, st.groups[id])
		if err != nil {
			// Every listener using it goes, so the configuration stays whole.
			for i := range st.listeners {
				if usesGroup(st.listeners[i].Spec, id) {
					problems[st.listeners[i].Metadata.UID] = err
				}
			}
			continue
		}
		cfg.TargetGroups = append(cfg.TargetGroups, g)
	}
	cfg.Listeners = slices.DeleteFunc(cfg.Listeners, func(e lbproxy.Listener) bool {
		uid, _, _ := strings.Cut(e.Name, "@")
		return problems[uid] != nil
	})
	slices.SortFunc(cfg.Listeners, func(a, b lbproxy.Listener) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(cfg.TargetGroups, func(a, b lbproxy.TargetGroup) int { return strings.Compare(a.Name, b.Name) })
	return cfg, problems
}

func usesGroup(spec resource.LBListenerSpec, id string) bool {
	return spec.DefaultTargetGroupID == id || slices.ContainsFunc(spec.Rules, func(r resource.LBListenerRule) bool { return r.TargetGroupID == id })
}

// listenerEntries returns the listener on each address of its load balancer.
func (r *ListenerReconciler) listenerEntries(st *l7Store, uid string, spec resource.LBListenerSpec, lb *resource.LoadBalancer) ([]lbproxy.Listener, error) {
	if lb.Status.Address == "" {
		return nil, pendingError("load balancer has no address yet")
	}
	if lb.Spec.Port == spec.Port {
		return nil, fmt.Errorf("port %d is the load balancer's layer-4 port; leave the load balancer's port unset", spec.Port)
	}
	for _, id := range append([]string{spec.DefaultTargetGroupID}, ruleGroups(spec)...) {
		g, ok := st.groups[id]
		if !ok {
			return nil, pendingError(fmt.Sprintf("target group %s not found", id))
		}
		if g.Spec.VPCID != lb.Spec.VPCID {
			return nil, fmt.Errorf("target group %s is not in the load balancer's vpc", id)
		}
	}
	var certs []lbproxy.Certificate
	for _, id := range spec.CertificateIDs {
		c, err := r.certificate(id)
		if err != nil {
			return nil, err
		}
		certs = append(certs, c)
	}
	rules := make([]lbproxy.Rule, 0, len(spec.Rules))
	for _, rule := range spec.Rules {
		rules = append(rules, lbproxy.Rule{Host: rule.Host, PathPrefix: rule.PathPrefix, TargetGroup: rule.TargetGroupID})
	}
	addrs := []string{lb.Status.Address}
	if r.edge && lb.Status.PublicAddress != "" {
		addrs = append(addrs, lb.Status.PublicAddress)
	}
	entries := make([]lbproxy.Listener, 0, len(addrs))
	for _, a := range addrs {
		entries = append(entries, lbproxy.Listener{
			Name:               listenerName(uid, a),
			Address:            a,
			Port:               spec.Port,
			Protocol:           spec.Protocol,
			TLSMode:            spec.TLSMode,
			Certificates:       certs,
			DefaultTargetGroup: spec.DefaultTargetGroupID,
			Rules:              rules,
		})
	}
	return entries, nil
}

func ruleGroups(spec resource.LBListenerSpec) []string {
	out := make([]string, 0, len(spec.Rules))
	for _, r := range spec.Rules {
		out = append(out, r.TargetGroupID)
	}
	return out
}

// certificate renders a certificate's chain and private key.
func (r *ListenerReconciler) certificate(id string) (lbproxy.Certificate, error) {
	if r.tls == nil {
		return lbproxy.Certificate{}, errors.New("no KMS key on this agent to render certificates")
	}
	chain, err := r.tls.CertificatePart(id, resource.SSLPartChain)
	if errors.Is(err, ErrCertificatePending) {
		return lbproxy.Certificate{}, pendingError(fmt.Sprintf("certificate %s not issued yet", id))
	}
	if err != nil {
		return lbproxy.Certificate{}, fmt.Errorf("certificate %s: %w", id, err)
	}
	key, err := r.tls.CertificatePart(id, resource.SSLPartPrivateKey)
	if err != nil {
		return lbproxy.Certificate{}, fmt.Errorf("certificate %s key: %w", id, err)
	}
	return lbproxy.Certificate{CertPEM: string(chain), KeyPEM: string(key)}, nil
}

// targetGroup renders a target group with its targets that have an address.
func (r *ListenerReconciler) targetGroup(st *l7Store, g *resource.LBTargetGroup) (lbproxy.TargetGroup, error) {
	spec := g.Spec.WithDefaults()
	out := lbproxy.TargetGroup{
		Name:       g.Metadata.UID,
		Protocol:   spec.Protocol,
		ServerName: spec.ServerName,
		HealthCheck: lbproxy.HealthCheck{
			Protocol:           spec.HealthCheck.Protocol,
			Path:               spec.HealthCheck.Path,
			Port:               spec.HealthCheck.Port,
			IntervalSeconds:    spec.HealthCheck.IntervalSeconds,
			TimeoutSeconds:     spec.HealthCheck.TimeoutSeconds,
			HealthyThreshold:   spec.HealthCheck.HealthyThreshold,
			UnhealthyThreshold: spec.HealthCheck.UnhealthyThreshold,
		},
		Targets: []lbproxy.Target{},
	}
	if spec.BackendCAID != "" {
		if r.tls == nil {
			return out, errors.New("no KMS key on this agent to read the backend CA")
		}
		ca, err := r.tls.CACertificate(spec.BackendCAID)
		if err != nil {
			return out, fmt.Errorf("target group %s backend CA: %w", g.Metadata.UID, err)
		}
		out.BackendCAPEM = string(ca)
	}
	for i := range st.targets {
		t := &st.targets[i]
		c, ok := st.computes[t.Spec.ComputeID]
		if t.Metadata.IsDeleting() || t.Spec.TargetGroupID != g.Metadata.UID || !ok || c.Status.IP == "" {
			continue
		}
		ts := t.Spec.WithDefaults()
		port := ts.Port
		if port == 0 {
			port = spec.Port
		}
		out.Targets = append(out.Targets, lbproxy.Target{ID: t.Metadata.UID, Address: c.Status.IP, Port: port, Weight: ts.Weight})
	}
	slices.SortFunc(out.Targets, func(a, b lbproxy.Target) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// pendingError is a reason a listener waits rather than fails.
type pendingError string

func (e pendingError) Error() string { return string(e) }

// report records each listener's phase and each target's health on this
// host.
func (r *ListenerReconciler) report(st *l7Store, vpcID string, cfg *lbproxy.Config, status lbproxy.Status, problems map[string]error) {
	bound := map[string]lbproxy.ListenerStatus{}
	for _, ls := range status.Listeners {
		bound[ls.Name] = ls
	}
	for i := range st.listeners {
		l := &st.listeners[i]
		lb, ok := st.lbs[l.Spec.LoadBalancerID]
		if l.Metadata.IsDeleting() || !ok || lb.Spec.VPCID != vpcID {
			continue
		}
		phase, reason, msg := resource.PhaseReady, "Serving", "infra-lb serves it"
		var pending pendingError
		switch err := problems[l.Metadata.UID]; {
		case errors.As(err, &pending):
			phase, reason, msg = resource.PhasePending, "Waiting", err.Error()
		case err != nil:
			phase, reason, msg = resource.PhaseError, "ConfigError", err.Error()
		case status.LastError != "":
			phase, reason, msg = resource.PhaseError, "Rejected", status.LastError
		default:
			for _, e := range cfg.Listeners {
				if uid, _, _ := strings.Cut(e.Name, "@"); uid != l.Metadata.UID {
					continue
				}
				if s, ok := bound[e.Name]; !ok || !s.Bound {
					phase, reason, msg = resource.PhasePending, "Binding", fmt.Sprintf("%s:%d not bound yet %s", e.Address, e.Port, s.Error)
				}
			}
		}
		if l.Status.Phase == phase && l.Status.IsConverged(l.Metadata.Generation) {
			continue
		}
		l.Status.SetPhase(phase, reason, strings.TrimSpace(msg))
		if phase == resource.PhaseReady {
			l.Status.MarkReconciled(l.Metadata.Generation)
		}
		_ = r.listeners.Put(l)
	}

	for _, gs := range status.TargetGroups {
		g, ok := st.groups[gs.Name]
		if !ok {
			continue
		}
		mine := make([]resource.LBTargetHealth, 0, len(gs.Targets))
		for _, t := range gs.Targets {
			computeID := ""
			for i := range st.targets {
				if st.targets[i].Metadata.UID == t.ID {
					computeID = st.targets[i].Spec.ComputeID
				}
			}
			mine = append(mine, resource.LBTargetHealth{ComputeID: computeID, Address: t.Address, Port: t.Port, Health: t.Health, NodeName: r.nodeName})
		}
		// Each host reports what it sees; the other hosts' entries stay.
		merged := slices.DeleteFunc(slices.Clone(g.Status.Targets), func(h resource.LBTargetHealth) bool { return h.NodeName == r.nodeName })
		merged = append(merged, mine...)
		slices.SortFunc(merged, func(a, b resource.LBTargetHealth) int {
			return strings.Compare(a.NodeName+"/"+a.ComputeID, b.NodeName+"/"+b.ComputeID)
		})
		if slices.Equal(merged, g.Status.Targets) && g.Status.IsConverged(g.Metadata.Generation) {
			continue
		}
		g.Status.Targets = merged
		g.Status.MarkReconciled(g.Metadata.Generation)
		g.Status.SetPhase(resource.PhaseReady, "Checked", "targets checked by infra-lb")
		_ = r.groups.Put(g)
	}
}

// ExecDataPlane is the DataPlane running infra-lb@<namespace> with systemd,
// its configuration and status under dir/<namespace>.
type ExecDataPlane struct {
	run  Runner
	dir  string
	unit string // the systemd template, e.g. "infra-lb@"
}

// NewExecDataPlane returns a data plane keeping its files under /run/infra-lb,
// where infra-lb@.service reads and writes them.
func NewExecDataPlane() *ExecDataPlane {
	return &ExecDataPlane{run: defaultRun, dir: "/run/infra-lb", unit: "infra-lb@"}
}

// NewEgressDataPlane returns the data plane of the egress proxies, keeping
// its files under /run/infra-egress, where infra-egress@.service reads and
// writes them.
func NewEgressDataPlane() *ExecDataPlane {
	return &ExecDataPlane{run: defaultRun, dir: "/run/infra-egress", unit: "infra-egress@"}
}

// NewExecDataPlaneWith returns a data plane driven by run under dir, for
// tests, running unit (a template such as "infra-lb@").
func NewExecDataPlaneWith(run Runner, dir, unit string) *ExecDataPlane {
	return &ExecDataPlane{run: run, dir: dir, unit: unit}
}

func (p *ExecDataPlane) unitOf(namespace string) string { return p.unit + namespace + ".service" }

// Apply implements DataPlane.
func (p *ExecDataPlane) Apply(ctx context.Context, namespace string, cfg *lbproxy.Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("manager: encode infra-lb config: %w", err)
	}
	dir := filepath.Join(p.dir, namespace)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("manager: infra-lb dir: %w", err)
	}
	path := filepath.Join(dir, "config.json")
	current, err := os.ReadFile(path) // #nosec G304 -- agent-owned path
	changed := err != nil || !bytes.Equal(current, data)
	if changed {
		// The configuration holds private keys: 0600, written whole.
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			return fmt.Errorf("manager: write infra-lb config: %w", err)
		}
		if err := os.Rename(tmp, path); err != nil {
			return fmt.Errorf("manager: write infra-lb config: %w", err)
		}
	}
	unit := p.unitOf(namespace)
	if _, err := p.run(ctx, "systemctl", "is-active", "--quiet", unit); err != nil {
		if out, err := p.run(ctx, "systemctl", "start", unit); err != nil {
			return fmt.Errorf("manager: start %s: %w: %s", unit, err, strings.TrimSpace(out))
		}
		return nil
	}
	if changed {
		if out, err := p.run(ctx, "systemctl", "reload", unit); err != nil {
			return fmt.Errorf("manager: reload %s: %w: %s", unit, err, strings.TrimSpace(out))
		}
	}
	return nil
}

// Stop implements DataPlane.
func (p *ExecDataPlane) Stop(ctx context.Context, namespace string) error {
	unit := p.unitOf(namespace)
	if out, err := p.run(ctx, "systemctl", "stop", unit); err != nil && !strings.Contains(out, "not loaded") {
		return fmt.Errorf("manager: stop %s: %w: %s", unit, err, strings.TrimSpace(out))
	}
	if err := os.RemoveAll(filepath.Join(p.dir, namespace)); err != nil {
		return fmt.Errorf("manager: remove infra-lb dir: %w", err)
	}
	return nil
}

// Status implements DataPlane.
func (p *ExecDataPlane) Status(namespace string) (lbproxy.Status, bool) {
	var st lbproxy.Status
	data, err := os.ReadFile(filepath.Join(p.dir, namespace, "status.json")) // #nosec G304 -- agent-owned path
	if err != nil || json.Unmarshal(data, &st) != nil {
		return st, false
	}
	return st, true
}

// Serving implements DataPlane: it reads the namespaces back from their
// directories, which outlive the agent.
func (p *ExecDataPlane) Serving() []string {
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}
