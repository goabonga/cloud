// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package lbproxy

import (
	"context"
	"crypto/x509"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultDrain is how long a closing listener waits for in-flight requests
// and connections before cutting them.
const DefaultDrain = 10 * time.Second

// Proxy runs the listeners and target groups of the configuration it was
// last given, applying each new one as a diff: a listener whose address,
// port, protocol and TLS mode are unchanged keeps its socket and takes its
// new routes and certificates atomically, and a target keeping its group,
// address and port keeps its health.
type Proxy struct {
	log *slog.Logger
	// unit is the length of the health checks' "seconds"; tests shrink it.
	unit  time.Duration
	drain time.Duration

	// groups is read on every request and swapped whole by Apply.
	groups atomic.Pointer[map[string]*group]

	mu         sync.Mutex
	ctx        context.Context
	cancel     context.CancelFunc
	cfg        *Config
	generation int
	lastError  string
	states     map[string]*targetState
	checkers   map[string]*checkerHandle
	listeners  map[string]*listener
	closing    sync.WaitGroup
}

type checkerHandle struct {
	key    string // what the checker was started with
	cancel context.CancelFunc
}

// New returns a proxy serving nothing until Apply.
func New(log *slog.Logger) *Proxy {
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &Proxy{
		log:       log,
		unit:      time.Second,
		drain:     DefaultDrain,
		ctx:       ctx,
		cancel:    cancel,
		cfg:       &Config{},
		states:    map[string]*targetState{},
		checkers:  map[string]*checkerHandle{},
		listeners: map[string]*listener{},
	}
	empty := map[string]*group{}
	p.groups.Store(&empty)
	return p
}

// Apply makes cfg, already validated, the running configuration.
func (p *Proxy) Apply(cfg *Config) {
	p.mu.Lock()
	defer p.mu.Unlock()

	groups := make(map[string]*group, len(cfg.TargetGroups))
	keep := map[string]bool{}
	for _, gc := range cfg.TargetGroups {
		groups[gc.Name] = newGroup(gc, p.states, p.log)
		roots := backendRoots(gc)
		for _, t := range gc.Targets {
			key := stateKey(gc.Name, t.Address, t.Port)
			keep[key] = true
			p.ensureChecker(key, gc, t, roots)
		}
	}
	for key, h := range p.checkers {
		if !keep[key] {
			h.cancel()
			delete(p.checkers, key)
			delete(p.states, key)
		}
	}
	old := p.groups.Swap(&groups)
	for _, g := range *old {
		g.closeIdle()
	}

	wanted := map[string]bool{}
	for _, lc := range cfg.Listeners {
		wanted[lc.Name] = true
		cur := p.listeners[lc.Name]
		if cur != nil && cur.bindKey() == bindKey(lc) {
			cur.update(lc)
			continue
		}
		if cur != nil {
			p.closeAsync(cur)
		}
		l := newListener(p, lc)
		l.bind()
		p.listeners[lc.Name] = l
	}
	for name, l := range p.listeners {
		if !wanted[name] {
			p.closeAsync(l)
			delete(p.listeners, name)
		}
	}
	p.cfg = cfg
	p.generation++
	p.lastError = ""
	p.log.Info("configuration applied", "generation", p.generation,
		"listeners", len(cfg.Listeners), "targetGroups", len(cfg.TargetGroups))
}

// ensureChecker starts the health checker of a target, or restarts it when
// its check changed. The target's state carries over either way.
func (p *Proxy) ensureChecker(key string, gc TargetGroup, t Target, roots *x509.CertPool) {
	hc := gc.HealthCheck
	params := key + "|" + hc.Protocol + "|" + hc.Path + "|" + gc.BackendCAPEM + "|" + gc.ServerName + "|" +
		itoa(hc.Port) + "|" + itoa(hc.IntervalSeconds) + "|" + itoa(hc.TimeoutSeconds) + "|" +
		itoa(hc.HealthyThreshold) + "|" + itoa(hc.UnhealthyThreshold)
	if h := p.checkers[key]; h != nil {
		if h.key == params {
			return
		}
		h.cancel()
	}
	ctx, cancel := context.WithCancel(p.ctx)
	c := &checker{group: gc.Name, hc: hc, target: t, roots: roots, server: gc.ServerName, state: p.states[key], unit: p.unit}
	go c.run(ctx)
	p.checkers[key] = &checkerHandle{key: params, cancel: cancel}
}

// Fail records a configuration that could not be applied; the running one
// stays.
func (p *Proxy) Fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastError = err.Error()
	p.log.Error("configuration rejected, keeping the running one", "generation", p.generation, "err", err)
}

// Generation counts the configurations applied since start.
func (p *Proxy) Generation() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.generation
}

// ClearError forgets the rejection of a configuration, once the running one
// is back in the file.
func (p *Proxy) ClearError() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastError = ""
}

// RetryBinds binds again the listeners whose address could not be bound,
// e.g. before it was assigned.
func (p *Proxy) RetryBinds() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, l := range p.listeners {
		if !l.bound() {
			l.bind()
		}
	}
}

// closeAsync stops l accepting now and drains it in the background.
func (p *Proxy) closeAsync(l *listener) {
	p.closing.Add(1)
	go func() {
		defer p.closing.Done()
		l.close(p.drain)
	}()
}

// Shutdown stops every listener and checker, draining in-flight work for at
// most the drain period.
func (p *Proxy) Shutdown() {
	p.mu.Lock()
	for name, l := range p.listeners {
		p.closeAsync(l)
		delete(p.listeners, name)
	}
	p.cancel()
	p.mu.Unlock()
	p.closing.Wait()
	for _, g := range *p.groups.Load() {
		g.closeIdle()
	}
}

// group returns the running target group called name.
func (p *Proxy) group(name string) *group { return (*p.groups.Load())[name] }

// transport returns the group's HTTP transport towards serverName, creating
// it on first use. Groups are rebuilt on reload, transports with them.
func (g *group) transport(serverName string) *http.Transport {
	g.trMu.Lock()
	defer g.trMu.Unlock()
	if g.transports == nil {
		g.transports = map[string]*http.Transport{}
	}
	if tr := g.transports[serverName]; tr != nil {
		return tr
	}
	tr := &http.Transport{
		DialContext:           dialer.DialContext,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if g.cfg.Protocol == ProtocolHTTPS {
		tr.TLSClientConfig = backendTLS(backendRoots(g.cfg), serverName)
	}
	g.transports[serverName] = tr
	return tr
}

func (g *group) closeIdle() {
	g.trMu.Lock()
	defer g.trMu.Unlock()
	for _, tr := range g.transports {
		tr.CloseIdleConnections()
	}
}
