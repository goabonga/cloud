// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/manager"
)

// ipSim is a minimal in-memory stand-in for the `ip` command, used to drive the
// ExecBackend without touching the kernel.
type ipSim struct {
	exist map[string]bool
	calls [][]string
}

func newIPSim(existing ...string) *ipSim {
	s := &ipSim{exist: make(map[string]bool)}
	for _, n := range existing {
		s.exist[n] = true
	}
	return s
}

func (s *ipSim) run(_ context.Context, name string, args ...string) (string, error) {
	s.calls = append(s.calls, append([]string{name}, args...))
	if name != "ip" {
		return "", errors.New("unexpected command: " + name)
	}
	switch {
	case len(args) >= 3 && args[0] == "link" && args[1] == "show":
		dev := args[2]
		if s.exist[dev] {
			return dev + ": <BROADCAST> mtu 1500", nil
		}
		return "Cannot find device " + dev, errors.New("exit status 1")
	case len(args) >= 5 && args[0] == "link" && args[1] == "add":
		s.exist[args[3]] = true // ip link add name <dev> type bridge
		return "", nil
	case len(args) >= 4 && args[0] == "link" && args[1] == "set" && args[3] == "up":
		return "", nil
	case len(args) >= 3 && args[0] == "link" && args[1] == "del":
		delete(s.exist, args[2])
		return "", nil
	default:
		return "", errors.New("unhandled ip args: " + strings.Join(args, " "))
	}
}

func TestExecBackendBridgeExists(t *testing.T) {
	t.Parallel()

	sim := newIPSim("br-1")
	be := manager.NewExecBackendWithRunner(sim.run)

	if ok, err := be.BridgeExists(context.Background(), "br-1"); err != nil || !ok {
		t.Fatalf("br-1 exists: ok=%v err=%v", ok, err)
	}
	if ok, err := be.BridgeExists(context.Background(), "br-x"); err != nil || ok {
		t.Fatalf("br-x absent: ok=%v err=%v", ok, err)
	}
}

func TestExecBackendEnsureCreatesWhenAbsent(t *testing.T) {
	t.Parallel()

	sim := newIPSim()
	be := manager.NewExecBackendWithRunner(sim.run)

	if err := be.EnsureBridge(context.Background(), manager.Bridge{Name: "br-1", CIDR: "10.0.0.0/16"}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !sim.exist["br-1"] {
		t.Fatal("bridge should exist after ensure")
	}
	if !sawCall(sim.calls, "ip", "link", "add", "name", "br-1", "type", "bridge") {
		t.Fatalf("expected an add call, got %v", sim.calls)
	}
	if !sawCall(sim.calls, "ip", "link", "set", "br-1", "up") {
		t.Fatalf("expected a set-up call, got %v", sim.calls)
	}
}

func TestExecBackendEnsureSkipsAddWhenPresent(t *testing.T) {
	t.Parallel()

	sim := newIPSim("br-1")
	be := manager.NewExecBackendWithRunner(sim.run)

	if err := be.EnsureBridge(context.Background(), manager.Bridge{Name: "br-1"}); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	for _, c := range sim.calls {
		if len(c) >= 2 && c[1] == "add" {
			t.Fatalf("did not expect an add call, got %v", sim.calls)
		}
	}
}

func TestExecBackendDelete(t *testing.T) {
	t.Parallel()

	sim := newIPSim("br-1")
	be := manager.NewExecBackendWithRunner(sim.run)

	if err := be.DeleteBridge(context.Background(), "br-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if sim.exist["br-1"] {
		t.Fatal("bridge should be gone")
	}
	// Deleting an absent bridge is a no-op (no del call issued).
	sim.calls = nil
	if err := be.DeleteBridge(context.Background(), "br-1"); err != nil {
		t.Fatalf("delete absent: %v", err)
	}
	for _, c := range sim.calls {
		if len(c) >= 2 && c[1] == "del" {
			t.Fatalf("did not expect a del call, got %v", sim.calls)
		}
	}
}

func sawCall(calls [][]string, want ...string) bool {
	for _, c := range calls {
		if strings.Join(c, " ") == strings.Join(want, " ") {
			return true
		}
	}
	return false
}
