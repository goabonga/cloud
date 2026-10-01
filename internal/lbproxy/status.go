// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package lbproxy

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Status is what infra-lb reports of itself.
type Status struct {
	// ConfigGeneration counts the configurations applied since start.
	ConfigGeneration int `json:"configGeneration"`
	// LastError is why the latest configuration was rejected, if it was.
	LastError    string           `json:"lastError"`
	Listeners    []ListenerStatus `json:"listeners"`
	TargetGroups []GroupStatus    `json:"targetGroups"`
}

// ListenerStatus reports whether a listener holds its socket.
type ListenerStatus struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	Bound   bool   `json:"bound"`
	Error   string `json:"error,omitempty"`
}

// GroupStatus reports the health of a group's targets.
type GroupStatus struct {
	Name    string         `json:"name"`
	Targets []TargetStatus `json:"targets"`
}

// TargetStatus is one target's health.
type TargetStatus struct {
	ID        string `json:"id"`
	Address   string `json:"address"`
	Port      int    `json:"port"`
	Health    string `json:"health"`
	LastError string `json:"lastError,omitempty"`
}

// Status reports the running configuration's state, in its order.
func (p *Proxy) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := Status{ConfigGeneration: p.generation, LastError: p.lastError,
		Listeners: []ListenerStatus{}, TargetGroups: []GroupStatus{}}
	for _, lc := range p.cfg.Listeners {
		ls := ListenerStatus{Name: lc.Name, Address: lc.Address, Port: lc.Port}
		if l := p.listeners[lc.Name]; l != nil {
			ls.Bound, ls.Error = l.status()
		}
		st.Listeners = append(st.Listeners, ls)
	}
	for _, gc := range p.cfg.TargetGroups {
		gs := GroupStatus{Name: gc.Name, Targets: []TargetStatus{}}
		for _, t := range gc.Targets {
			ts := TargetStatus{ID: t.ID, Address: t.Address, Port: t.Port, Health: HealthUnknown}
			if s := p.states[stateKey(gc.Name, t.Address, t.Port)]; s != nil {
				ts.Health, ts.LastError = s.snapshot()
			}
			gs.Targets = append(gs.Targets, ts)
		}
		st.TargetGroups = append(st.TargetGroups, gs)
	}
	return st
}

// WriteStatus writes st to path atomically: a temporary file in the same
// directory, renamed over it, so a reader never sees half of one.
func WriteStatus(path string, st Status) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("lbproxy: status: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("lbproxy: status: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("lbproxy: status: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("lbproxy: status: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("lbproxy: status: %w", err)
	}
	return nil
}

// RunOptions drives Run.
type RunOptions struct {
	// ConfigPath is the configuration file, reloaded when it changes.
	ConfigPath string
	// StatusPath, when set, receives the status every Poll and after every
	// reload.
	StatusPath string
	// Poll is how often the configuration file is checked, the unbound
	// listeners bound again and the status written; a second by default.
	Poll time.Duration
	// Reload, when it fires, reloads the configuration at once (SIGHUP).
	Reload <-chan struct{}
}

// Run serves the configuration at opts.ConfigPath until ctx ends, then drains.
// A configuration that does not load is reported in the status and leaves the
// running one, possibly none yet, in place.
func Run(ctx context.Context, p *Proxy, opts RunOptions) error {
	if opts.ConfigPath == "" {
		return errors.New("lbproxy: no configuration path")
	}
	if opts.Poll <= 0 {
		opts.Poll = time.Second
	}
	var seen os.FileInfo
	var applied [sha256.Size]byte
	load := func(force bool) {
		fi, err := os.Stat(opts.ConfigPath)
		if err != nil {
			if seen != nil || force {
				p.Fail(err)
			}
			seen = nil
			return
		}
		if !force && seen != nil && fi.ModTime().Equal(seen.ModTime()) && fi.Size() == seen.Size() {
			return
		}
		seen = fi
		raw, err := os.ReadFile(opts.ConfigPath)
		if err != nil {
			p.Fail(err)
			return
		}
		// The running configuration, read again - the poll and SIGHUP both
		// noticing one write, or a rejected file put back: nothing to apply.
		sum := sha256.Sum256(raw)
		if sum == applied && p.Generation() > 0 {
			p.ClearError()
			return
		}
		cfg, err := ParseConfig(raw)
		if err != nil {
			p.Fail(err)
			return
		}
		p.Apply(cfg)
		applied = sum
	}
	write := func() {
		if opts.StatusPath == "" {
			return
		}
		if err := WriteStatus(opts.StatusPath, p.Status()); err != nil {
			p.log.Warn("status not written", "err", err)
		}
	}
	load(true)
	write()
	ticker := time.NewTicker(opts.Poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			p.Shutdown()
			write()
			return nil
		case <-opts.Reload:
			load(true)
		case <-ticker.C:
			load(false)
			p.RetryBinds()
		}
		write()
	}
}
