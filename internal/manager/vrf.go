// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Every VPC routes in a VRF of its own: its bridge is enslaved to a VRF
// device whose routing table holds the subnets' connected routes, the VIPs
// and, with an internet gateway, a default route to the host's uplink. Two
// VPCs never see each other's routes, so their CIDRs may overlap, and the
// host's main table no longer reaches into any VPC. The table ends with an
// unreachable default, so a lookup that finds nothing there never falls
// through to the main table.
//
// What crosses between a VPC and the host's main table is steered by marks:
//
//   - a connection an instance opens (to the Internet through the gateway)
//     is marked with the VPC's table, and the replies coming back from the
//     uplink get that mark restored, which a rule turns into a lookup in the
//     VPC's table rather than the main one;
//   - a connection opened from outside to a port an instance maps is marked
//     "back to main": its replies, leaving the VPC, are looked up in the main
//     table, while its packets towards the instance carry the VPC's table.
//
// The rules sit at priority 900, ahead of the l3mdev rule (1000) through
// which the kernel sends the VPC's own traffic to its table.

// vrfRulePref is the priority of the mark rules.
const vrfRulePref = "900"

// vrfUnreachableMetric is the metric of the VRF table's unreachable default,
// the highest there is, so any other default wins.
const vrfUnreachableMetric = "4278198272"

// vrfName names a VPC's VRF device.
func vrfName(uid string) string { return ifaceName("vrf-", uid) }

// vrfTable derives a VPC's routing table from its VNI, so every host computes
// the same one: 0x01000000 + VNI keeps clear of the reserved tables and of
// those an administrator numbers by hand.
func vrfTable(uid string) uint32 { return 0x01000000 | vniFor(uid) }

// vrfMark is the mark of the connections an instance of the VPC opens, equal
// to its table; vrfMainMark marks those opened towards it from outside, whose
// replies go back through the main table.
func vrfMark(uid string) uint32     { return vrfTable(uid) }
func vrfMainMark(uid string) uint32 { return vrfTable(uid) | 0x40000000 }

// vrfTableArg is vrfTable as an `ip ... table` argument.
func vrfTableArg(uid string) string { return strconv.FormatUint(uint64(vrfTable(uid)), 10) }

func hex32(v uint32) string { return "0x" + strconv.FormatUint(uint64(v), 16) }

// vrfMangleRules are the mangle PREROUTING rules of a VPC (see above). A
// packet from the VPC goes through PREROUTING twice, on the bridge and then on
// the VRF device, so the rules setting a packet's mark tell the replies from
// the rest by the connection's direction rather than by device: a mark on a
// packet bound for the host itself would send it through the mark rule
// rather than the VRF, and no socket bound to the VRF would receive it.
func vrfMangleRules(uid, bridge string) [][]string {
	mark, main := hex32(vrfMark(uid)), hex32(vrfMainMark(uid))
	return [][]string{
		// An instance opening a connection: mark it with the VPC's table.
		{"PREROUTING", "-i", bridge, "-m", "conntrack", "--ctstate", "NEW", "-m", "connmark", "--mark", "0",
			"-j", "CONNMARK", "--set-mark", mark},
		// Its replies, coming in from outside: look the VPC's table up.
		{"PREROUTING", "-m", "connmark", "--mark", mark, "-m", "conntrack", "--ctdir", "REPLY",
			"-j", "MARK", "--set-mark", mark},
		// Replies to a connection opened from outside: look the main table up.
		{"PREROUTING", "-m", "connmark", "--mark", main, "-m", "conntrack", "--ctdir", "REPLY",
			"-j", "MARK", "--set-mark", main},
	}
}

// iptablesEnsure appends rule to table unless present.
func iptablesEnsure(ctx context.Context, run Runner, table string, rule []string) error {
	check := append([]string{"-t", table, "-C"}, rule...)
	if _, err := run(ctx, "iptables", check...); err == nil {
		return nil
	}
	add := append([]string{"-t", table, "-A"}, rule...)
	if out, err := run(ctx, "iptables", add...); err != nil {
		return fmt.Errorf("manager: iptables %v: %w: %s", add, err, strings.TrimSpace(out))
	}
	return nil
}

// maxDuplicates bounds how many copies of a rule a delete removes: a rule is
// only ever added after checking for it, so copies come from races at most.
const maxDuplicates = 4

// iptablesDelete removes rule from table, ignoring an absent one.
func iptablesDelete(ctx context.Context, run Runner, table string, rule []string) {
	for range maxDuplicates {
		if _, err := run(ctx, "iptables", append([]string{"-t", table, "-D"}, rule...)...); err != nil {
			return
		}
	}
}

// ensureRule adds the policy rule `fwmark mark lookup table` unless present.
func ensureRule(ctx context.Context, run Runner, mark uint32, table string) error {
	out, err := run(ctx, "ip", "rule", "show", "pref", vrfRulePref)
	if err != nil {
		return fmt.Errorf("manager: show rules: %w: %s", err, strings.TrimSpace(out))
	}
	want := "fwmark " + hex32(mark) + " lookup " + table
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, want) {
			return nil
		}
	}
	if out, err := run(ctx, "ip", "rule", "add", "pref", vrfRulePref, "fwmark", hex32(mark), "lookup", table); err != nil {
		return fmt.Errorf("manager: add rule %s: %w: %s", want, err, strings.TrimSpace(out))
	}
	return nil
}

// deleteRule removes the policy rule `fwmark mark lookup table`, if present.
func deleteRule(ctx context.Context, run Runner, mark uint32, table string) {
	for range maxDuplicates {
		if _, err := run(ctx, "ip", "rule", "del", "pref", vrfRulePref, "fwmark", hex32(mark), "lookup", table); err != nil {
			return
		}
	}
}

// EnsureVRF puts the VPC's bridge in a VRF of its own, with the table's
// unreachable default and the mark rules. Idempotent.
func (b *ExecBackend) EnsureVRF(ctx context.Context, vpcID, bridge string) error {
	vrf, table := vrfName(vpcID), vrfTableArg(vpcID)
	exists, err := linkExists(ctx, b.run, vrf)
	if err != nil {
		return err
	}
	steps := [][]string{}
	if !exists {
		steps = append(steps, []string{"ip", "link", "add", vrf, "type", "vrf", "table", table})
	}
	steps = append(steps,
		[]string{"ip", "link", "set", vrf, "up"},
		[]string{"ip", "route", "replace", "unreachable", "default", "metric", vrfUnreachableMetric, "table", table},
		[]string{"ip", "link", "set", bridge, "master", vrf},
	)
	if err := runSteps(ctx, b.run, "vrf", steps); err != nil {
		return err
	}
	if err := ensureRule(ctx, b.run, vrfMark(vpcID), table); err != nil {
		return err
	}
	if err := ensureRule(ctx, b.run, vrfMainMark(vpcID), "main"); err != nil {
		return err
	}
	for _, rule := range vrfMangleRules(vpcID, bridge) {
		if err := iptablesEnsure(ctx, b.run, "mangle", rule); err != nil {
			return err
		}
	}
	return nil
}

// DeleteVRF removes the VPC's VRF device, its table's routes and the mark
// rules. Removing an absent VRF is not an error.
func (b *ExecBackend) DeleteVRF(ctx context.Context, vpcID, bridge string) error {
	for _, rule := range vrfMangleRules(vpcID, bridge) {
		iptablesDelete(ctx, b.run, "mangle", rule)
	}
	table := vrfTableArg(vpcID)
	deleteRule(ctx, b.run, vrfMark(vpcID), table)
	deleteRule(ctx, b.run, vrfMainMark(vpcID), "main")
	_, _ = b.run(ctx, "ip", "route", "flush", "table", table)
	out, err := b.run(ctx, "ip", "link", "del", vrfName(vpcID))
	if err != nil && !strings.Contains(out, "Cannot find device") && !strings.Contains(out, "does not exist") {
		return fmt.Errorf("manager: delete vrf %q: %w: %s", vrfName(vpcID), err, strings.TrimSpace(out))
	}
	return nil
}

// EnsureEgress gives the VPC's table the host's default route, so its
// instances reach the uplink. Idempotent.
func (b *ExecBackend) EnsureEgress(ctx context.Context, vpcID string) error {
	out, err := b.run(ctx, "ip", "route", "show", "default")
	if err != nil {
		return fmt.Errorf("manager: default route: %w: %s", err, strings.TrimSpace(out))
	}
	route := defaultRouteArgs(out)
	if route == nil {
		return fmt.Errorf("manager: the host has no default route to give vpc %s", vpcID)
	}
	args := append([]string{"route", "replace", "default"}, route...)
	args = append(args, "table", vrfTableArg(vpcID))
	if out, err := b.run(ctx, "ip", args...); err != nil {
		return fmt.Errorf("manager: vpc %s default route: %w: %s", vpcID, err, strings.TrimSpace(out))
	}
	return nil
}

// DeleteEgress removes the VPC's default route, leaving the unreachable one.
func (b *ExecBackend) DeleteEgress(ctx context.Context, vpcID string) error {
	table := vrfTableArg(vpcID)
	for range maxDuplicates {
		out, err := b.run(ctx, "ip", "route", "show", "default", "table", table)
		if err != nil {
			return nil
		}
		line := ""
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.TrimSpace(l), "default ") {
				line = strings.TrimSpace(l)
				break
			}
		}
		if line == "" {
			return nil
		}
		args := append([]string{"route", "del"}, strings.Fields(line)...)
		if _, err := b.run(ctx, "ip", append(args, "table", table)...); err != nil {
			return nil
		}
	}
	return nil
}

// defaultRouteArgs extracts "via <gw> dev <iface>" (or "dev <iface>") from
// the first line of `ip route show default`.
func defaultRouteArgs(out string) []string {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	fields := strings.Fields(line)
	var via, dev string
	for i := 0; i+1 < len(fields); i++ {
		switch fields[i] {
		case "via":
			via = fields[i+1]
		case "dev":
			dev = fields[i+1]
		}
	}
	switch {
	case dev == "":
		return nil
	case via == "":
		return []string{"dev", dev}
	default:
		return []string{"via", via, "dev", dev}
	}
}
