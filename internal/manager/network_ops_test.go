// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/goabonga/infrastructure/internal/manager"
)

// netRecorder records commands; iptables -C checks report "not present" so the
// add path runs, and `ip route show default` returns a canned line.
type netRecorder struct {
	calls    [][]string
	routeOut string
}

func (r *netRecorder) run(_ context.Context, name string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	if name == "ip" && len(args) >= 3 && args[0] == "route" && args[1] == "show" && args[2] == "default" {
		return r.routeOut, nil
	}
	for _, a := range args {
		if a == "-C" {
			return "", errors.New("rule does not exist")
		}
	}
	return "", nil
}

func TestExecBackendNetworkOps(t *testing.T) {
	t.Parallel()

	rec := &netRecorder{routeOut: "default via 192.168.1.1 dev wan0 proto dhcp src 192.168.1.50"}
	be := manager.NewExecBackendWithRunner(rec.run)
	ctx := context.Background()

	ifc, err := be.DefaultInterface(ctx)
	if err != nil || ifc != "wan0" {
		t.Fatalf("DefaultInterface = %q, %v", ifc, err)
	}
	if err := be.EnsureAddress(ctx, "br0", "10.0.1.1/24"); err != nil {
		t.Fatalf("EnsureAddress: %v", err)
	}
	if err := be.EnableForwarding(ctx); err != nil {
		t.Fatalf("EnableForwarding: %v", err)
	}
	if err := be.EnsureNAT(ctx, "10.0.0.0/16", "wan0"); err != nil {
		t.Fatalf("EnsureNAT: %v", err)
	}

	for _, want := range [][]string{
		{"ip", "addr", "replace", "10.0.1.1/24", "dev", "br0"},
		{"sysctl", "-w", "net.ipv4.ip_forward=1"},
		{"iptables", "-t", "nat", "-A", "POSTROUTING", "-s", "10.0.0.0/16", "-o", "wan0", "-j", "MASQUERADE"},
	} {
		if !sawCall(rec.calls, want...) {
			t.Fatalf("missing call %v in %v", want, rec.calls)
		}
	}
}

// addrKernel mimics how a current kernel answers iproute2 for addresses:
// `ip addr add` of an address already on the interface fails with the extack
// message "Address already assigned" (not the older "File exists"), while
// `ip addr replace` succeeds whether or not it is there. Every other command
// succeeds.
type addrKernel struct {
	assigned map[string]bool
}

func (k *addrKernel) run(_ context.Context, name string, args ...string) (string, error) {
	if name != "ip" || len(args) < 5 || args[0] != "addr" {
		return "", nil
	}
	key := args[2] + " " + args[4]
	switch args[1] {
	case "add":
		if k.assigned[key] {
			return "Error: ipv4: Address already assigned.", errors.New("exit status 2")
		}
		k.assigned[key] = true
	case "replace":
		k.assigned[key] = true
	}
	return "", nil
}

func TestEnsureAddressIsIdempotent(t *testing.T) {
	t.Parallel()

	k := &addrKernel{assigned: map[string]bool{}}
	be := manager.NewExecBackendWithRunner(k.run)
	for pass := 1; pass <= 2; pass++ {
		if err := be.EnsureAddress(context.Background(), "br0", "10.0.1.1/24"); err != nil {
			t.Fatalf("pass %d: EnsureAddress: %v", pass, err)
		}
	}
}

func TestEnsureServiceVIPIsIdempotent(t *testing.T) {
	t.Parallel()

	k := &addrKernel{assigned: map[string]bool{}}
	be := manager.NewExecLBWithRunner(k.run)
	servers := []manager.LBRealServer{{IP: "10.0.1.10", Port: 8080, Weight: 1}}
	for pass := 1; pass <= 2; pass++ {
		if err := be.EnsureService(context.Background(), "10.0.5.5", 443, "tcp", "round_robin", "br-vpc1", servers); err != nil {
			t.Fatalf("pass %d: EnsureService: %v", pass, err)
		}
	}
}

// addrTable answers `ip -o addr show` from a set of assigned addresses and
// fails `ip addr del` of an absent one the way a current kernel does.
type addrTable struct {
	assigned map[string]bool // "iface addr/prefix"
	dels     []string
}

func (a *addrTable) run(_ context.Context, name string, args ...string) (string, error) {
	switch {
	case name == "ip" && len(args) == 5 && args[0] == "-o" && args[1] == "addr" && args[2] == "show":
		var out strings.Builder
		for k := range a.assigned {
			if iface, addr, _ := strings.Cut(k, " "); iface == args[4] {
				fmt.Fprintf(&out, "5: %s    inet %s scope global %s\\       valid_lft forever\n", iface, addr, iface)
			}
		}
		return out.String(), nil
	case name == "ip" && len(args) == 5 && args[0] == "addr" && args[1] == "del":
		key := args[4] + " " + args[2]
		a.dels = append(a.dels, key)
		if !a.assigned[key] {
			return "Error: ipv4: Address not found.", errors.New("exit status 2")
		}
		delete(a.assigned, key)
	}
	return "", nil
}

func TestDeleteAddressIsIdempotent(t *testing.T) {
	t.Parallel()

	a := &addrTable{assigned: map[string]bool{"np0 10.0.1.254/24": true}}
	be := manager.NewExecBackendWithRunner(a.run)
	for pass := 1; pass <= 2; pass++ {
		if err := be.DeleteAddress(context.Background(), "np0", "10.0.1.254/24"); err != nil {
			t.Fatalf("pass %d: DeleteAddress: %v", pass, err)
		}
	}
	// An address that was never there.
	if err := be.DeleteAddress(context.Background(), "np0", "10.0.1.253/24"); err != nil {
		t.Fatalf("absent address: %v", err)
	}
	if len(a.dels) != 1 {
		t.Fatalf("ip addr del should only run for the present address: %v", a.dels)
	}
}
