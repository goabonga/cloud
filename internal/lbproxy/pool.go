// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package lbproxy

import (
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
)

// targetState is the health of one backend. It is keyed by group name,
// address and port, and outlives a reload that keeps those.
type targetState struct {
	mu        sync.Mutex
	health    string
	successes int // consecutive successful active checks
	failures  int // consecutive failed active checks
	passive   int // consecutive failed connections or 5xx answers
	lastError string
}

func newTargetState() *targetState { return &targetState{health: HealthUnknown} }

// eligible reports whether traffic may go to the target: an unknown target
// counts as healthy until checks decide.
func (s *targetState) eligible() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.health != HealthUnhealthy
}

// observeCheck records an active check, moving the target to healthy after
// healthy consecutive successes and to unhealthy after unhealthy failures.
func (s *targetState) observeCheck(err error, healthy, unhealthy int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		s.successes++
		s.failures = 0
		s.lastError = ""
		if s.successes >= healthy {
			s.health = HealthHealthy
			s.passive = 0
		}
		return
	}
	s.failures++
	s.successes = 0
	s.lastError = err.Error()
	if s.failures >= unhealthy {
		s.health = HealthUnhealthy
	}
}

// observeTraffic records the outcome of proxied traffic: unhealthy
// consecutive failures eject the target until active checks bring it back.
func (s *targetState) observeTraffic(err error, unhealthy int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		s.passive = 0
		return
	}
	s.passive++
	s.lastError = err.Error()
	if s.passive >= unhealthy {
		s.health = HealthUnhealthy
		s.successes = 0
	}
}

func (s *targetState) snapshot() (health, lastError string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.health, s.lastError
}

// target is a backend of a group as configured, with its shared state.
type target struct {
	Target
	state *targetState
	// current is the smooth weighted round-robin counter.
	current int
}

func (t *target) addr() string { return net.JoinHostPort(t.Address, strconv.Itoa(t.Port)) }

// group is a configured target group and its balancer.
type group struct {
	cfg     TargetGroup
	log     *slog.Logger
	mu      sync.Mutex
	targets []*target
	// failingOpen is set while no target is eligible, so the switch to
	// sending everywhere is logged once.
	failingOpen bool

	trMu       sync.Mutex
	transports map[string]*http.Transport // by backend server name
}

func stateKey(group, address string, port int) string {
	return group + "|" + net.JoinHostPort(address, strconv.Itoa(port))
}

// newGroup builds a group, reusing from states the state of targets it keeps.
func newGroup(cfg TargetGroup, states map[string]*targetState, log *slog.Logger) *group {
	g := &group{cfg: cfg, log: log}
	for _, t := range cfg.Targets {
		key := stateKey(cfg.Name, t.Address, t.Port)
		st := states[key]
		if st == nil {
			st = newTargetState()
			states[key] = st
		}
		g.targets = append(g.targets, &target{Target: t, state: st})
	}
	return g
}

// pick chooses a target by smooth weighted round robin among the eligible
// ones not in exclude. With none eligible it fails open over every target
// not excluded. It returns nil when nothing is left.
func (g *group) pick(exclude map[*target]bool) *target {
	g.mu.Lock()
	defer g.mu.Unlock()
	var candidates []*target
	for _, t := range g.targets {
		if !exclude[t] && t.state.eligible() {
			candidates = append(candidates, t)
		}
	}
	if len(candidates) == 0 {
		for _, t := range g.targets {
			if !exclude[t] {
				candidates = append(candidates, t)
			}
		}
		if len(candidates) > 0 && !g.failingOpen {
			g.failingOpen = true
			g.log.Warn("no healthy target, failing open", "group", g.cfg.Name)
		}
	} else if g.failingOpen {
		g.failingOpen = false
		g.log.Info("healthy targets back", "group", g.cfg.Name)
	}
	if len(candidates) == 0 {
		return nil
	}
	total := 0
	var best *target
	for _, t := range candidates {
		t.current += t.Weight
		total += t.Weight
		if best == nil || t.current > best.current {
			best = t
		}
	}
	best.current -= total
	return best
}
