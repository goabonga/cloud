// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// vrfHost answers like a host with no VRF, rule or iptables rule yet, and
// records every command.
type vrfHost struct {
	calls  []string
	routes string // `ip route show default` in the main table
}

func (h *vrfHost) run(_ context.Context, name string, args ...string) (string, error) {
	cmd := name + " " + strings.Join(args, " ")
	h.calls = append(h.calls, cmd)
	switch {
	case strings.HasPrefix(cmd, "ip link show"):
		return "Device does not exist", errors.New("exit status 1")
	case strings.HasPrefix(cmd, "iptables -t mangle -C"):
		return "", errors.New("exit status 1")
	case cmd == "ip route show default":
		return h.routes, nil
	}
	return "", nil
}

func TestEnsureVRFIsolatesTheVPC(t *testing.T) {
	t.Parallel()

	h := &vrfHost{}
	if err := NewExecBackendWithRunner(h.run).EnsureVRF(context.Background(), "vpc1", "br-vpc1"); err != nil {
		t.Fatal(err)
	}
	table := vrfTableArg("vpc1")
	mark, main := hex32(vrfMark("vpc1")), hex32(vrfMainMark("vpc1"))
	for _, want := range []string{
		"ip link add vrf-vpc1 type vrf table " + table,
		"ip link set vrf-vpc1 up",
		"ip route replace unreachable default metric 4278198272 table " + table,
		"ip link set br-vpc1 master vrf-vpc1",
		"ip rule add pref 900 fwmark " + mark + " lookup " + table,
		"ip rule add pref 900 fwmark " + main + " lookup main",
		"iptables -t mangle -A PREROUTING -i br-vpc1 -m conntrack --ctstate NEW -m connmark --mark 0 -j CONNMARK --set-mark " + mark,
		"iptables -t mangle -A PREROUTING -m connmark --mark " + mark + " -m conntrack --ctdir REPLY -j MARK --set-mark " + mark,
		"iptables -t mangle -A PREROUTING -m connmark --mark " + main + " -m conntrack --ctdir REPLY -j MARK --set-mark " + main,
	} {
		if !slices.Contains(h.calls, want) {
			t.Fatalf("missing %q in\n%s", want, strings.Join(h.calls, "\n"))
		}
	}
	// No rule may set a packet mark by device alone: a packet for the host
	// itself goes through PREROUTING again on the VRF device.
	for _, c := range h.calls {
		if strings.Contains(c, "-j MARK") && !strings.Contains(c, "--ctdir REPLY") {
			t.Fatalf("packet mark set outside replies: %q", c)
		}
	}
}

func TestEnsureVRFLeavesExistingRulesAlone(t *testing.T) {
	t.Parallel()

	table := vrfTableArg("vpc1")
	existing := "900:\tfrom all fwmark " + hex32(vrfMark("vpc1")) + " lookup " + table + "\n" +
		"900:\tfrom all fwmark " + hex32(vrfMainMark("vpc1")) + " lookup main\n"
	var calls []string
	run := func(_ context.Context, name string, args ...string) (string, error) {
		cmd := name + " " + strings.Join(args, " ")
		calls = append(calls, cmd)
		if strings.HasPrefix(cmd, "ip rule show") {
			return existing, nil
		}
		return "", nil
	}
	if err := NewExecBackendWithRunner(run).EnsureVRF(context.Background(), "vpc1", "br-vpc1"); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "ip rule add") || strings.HasPrefix(c, "ip link add") || strings.HasPrefix(c, "iptables -t mangle -A") {
			t.Fatalf("%q re-added what exists", c)
		}
	}
}

func TestVRFTablesDifferPerVPCAndAvoidReservedOnes(t *testing.T) {
	t.Parallel()

	a, b := vrfTable("vpc-a"), vrfTable("vpc-b")
	if a == b || a < 0x01000000 || b < 0x01000000 {
		t.Fatalf("tables %d and %d", a, b)
	}
	if vrfMainMark("vpc-a") == vrfMark("vpc-a") {
		t.Fatal("the two marks of a VPC must differ")
	}
}

func TestEnsureEgressCopiesTheHostDefaultRoute(t *testing.T) {
	t.Parallel()

	h := &vrfHost{routes: "default via 192.168.122.1 dev enp1s0 proto static\n"}
	if err := NewExecBackendWithRunner(h.run).EnsureEgress(context.Background(), "vpc1"); err != nil {
		t.Fatal(err)
	}
	want := "ip route replace default via 192.168.122.1 dev enp1s0 table " + vrfTableArg("vpc1")
	if !slices.Contains(h.calls, want) {
		t.Fatalf("missing %q in %v", want, h.calls)
	}
	if err := NewExecBackendWithRunner((&vrfHost{}).run).EnsureEgress(context.Background(), "vpc1"); err == nil {
		t.Fatal("a host without a default route has none to give")
	}
}

func TestDefaultRouteArgs(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"default via 10.0.0.1 dev eth0 proto dhcp metric 100\ndefault via 10.0.0.2 dev eth1": "via 10.0.0.1 dev eth0",
		"default dev wg0 scope link": "dev wg0",
		"":                           "",
	} {
		if got := strings.Join(defaultRouteArgs(in), " "); got != want {
			t.Fatalf("defaultRouteArgs(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestComputeRulesMatchTheBridgeAndMarkPortMaps(t *testing.T) {
	t.Parallel()

	rules := computeRules("vpc1", "br-vpc1", "10.0.1.10", "INFRA-SG-1", []string{"8080:80"})
	var joined []string
	for _, r := range rules {
		joined = append(joined, strings.Join(r, " "))
	}
	for _, want := range []string{
		"filter FORWARD -o br-vpc1 -d 10.0.1.10 -j INFRA-SG-1",
		"filter OUTPUT -o br-vpc1 -d 10.0.1.10 -j INFRA-SG-1",
		"mangle PREROUTING ! -i br-vpc1 -p tcp --dport 8080 -m conntrack --ctstate NEW -j CONNMARK --set-mark " + hex32(vrfMainMark("vpc1")),
		"mangle PREROUTING ! -i br-vpc1 -p tcp --dport 8080 -j MARK --set-mark " + hex32(vrfMark("vpc1")),
		"nat PREROUTING -p tcp --dport 8080 -j DNAT --to-destination 10.0.1.10:80",
		"filter FORWARD -o br-vpc1 -p tcp -d 10.0.1.10 --dport 80 -j ACCEPT",
	} {
		if !slices.Contains(joined, want) {
			t.Fatalf("missing %q in\n%s", want, strings.Join(joined, "\n"))
		}
	}
}
