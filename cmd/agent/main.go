// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Command infra-agent reconciles the resources in the local store against the
// host kernel. It requires root / CAP_NET_ADMIN to manage bridges.
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
	"github.com/goabonga/infrastructure/internal/meta"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func main() {
	if err := run(); err != nil {
		slog.Error("infra-agent stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	stateDir := flag.String("state-dir", envOr("GOA_STATE_DIR", "./state"), "state directory")
	stateDSN := flag.String("state-dsn", envOr("GOA_STATE_DSN", ""), "PostgreSQL DSN (enables the HA backend)")
	interval := flag.Duration("interval", 5*time.Second, "reconcile interval")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	logger.Info(meta.Line("infra-agent", Version), "stateDir", *stateDir, "interval", interval.String())

	store, err := state.Open(*stateDir, *stateDSN)
	if err != nil {
		return err
	}
	var master []byte
	if raw := os.Getenv("GOA_KMS_KEY"); raw != "" {
		master, err = base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return err
		}
	}

	net := manager.NewExecBackend()
	vpcs := registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC)
	subnets := registry.New[resource.SubnetSpec, resource.SubnetStatus](store, resource.KindSubnet)
	igws := registry.New[resource.IGWSpec, resource.IGWStatus](store, resource.KindIGW)
	disks := registry.New[resource.DiskSpec, resource.DiskStatus](store, resource.KindDisk)
	acls := registry.New[resource.ACLPolicySpec, resource.ACLPolicyStatus](store, resource.KindACLPolicy)
	sgs := registry.New[resource.SecurityGroupSpec, resource.SecurityGroupStatus](store, resource.KindSecurityGroup)
	sgRules := registry.New[resource.SecurityGroupRuleSpec, resource.SecurityGroupRuleStatus](store, resource.KindSecurityGroupRule)
	computes := registry.New[resource.ComputeSpec, resource.ComputeStatus](store, resource.KindCompute)
	peerings := registry.New[resource.PeeringSpec, resource.PeeringStatus](store, resource.KindPeering)
	wafPolicies := registry.New[resource.WAFPolicySpec, resource.WAFPolicyStatus](store, resource.KindWAFPolicy)
	wafRules := registry.New[resource.WAFRuleSpec, resource.WAFRuleStatus](store, resource.KindWAFRule)
	dnsZones := registry.New[resource.DNSZoneSpec, resource.DNSZoneStatus](store, resource.KindDNSZone)
	dnsRecords := registry.New[resource.DNSRecordSpec, resource.DNSRecordStatus](store, resource.KindDNSRecord)
	lbs := registry.New[resource.LoadBalancerSpec, resource.LoadBalancerStatus](store, resource.KindLoadBalancer)
	lbBackends := registry.New[resource.LBBackendSpec, resource.LBBackendStatus](store, resource.KindLBBackend)
	nodes := registry.New[resource.NodeSpec, resource.NodeStatus](store, resource.KindNode)
	diskFiles := registry.New[resource.DiskFileSpec, resource.DiskFileStatus](store, resource.KindDiskFile)
	nodeID := os.Getenv("GOA_NODE_ID")
	agent := manager.NewAgent(*interval, logger,
		manager.NewNodeHeartbeat(nodes, nodeID),
		manager.NewVPCReconciler(vpcs, net),
		manager.NewOverlayReconciler(vpcs, nodes, manager.NewExecOverlay(), nodeID),
		manager.NewSubnetReconciler(subnets, vpcs, net).WithNodeIdentity(nodes, nodeID),
		manager.NewIGWReconciler(igws, vpcs, net),
		manager.NewPeeringReconciler(peerings, vpcs, manager.NewExecPeering()),
		manager.NewDNSReconciler(dnsZones, dnsRecords, vpcs, manager.NewExecDNS(filepath.Join(*stateDir, "dns"))),
		manager.NewDiskReconciler(disks, manager.NewExecDiskBackend(filepath.Join(*stateDir, "disks")), master),
		manager.NewSecurityGroupReconciler(sgs, sgRules, manager.NewExecSecurityGroup()),
		manager.NewACLReconciler(acls, manager.NewExecFirewall()),
		manager.NewComputeReconciler(computes, subnets, vpcs, disks, sgs, manager.NewExecComputeBackend(*stateDir), nodeID).WithAddressStore(store),
		manager.NewDiskFileReconciler(diskFiles, computes, manager.FSDiskFileWriter{}, nodeID),
		manager.NewWAFReconciler(wafPolicies, wafRules, computes, subnets, igws, vpcs, manager.NewExecWAF()),
		manager.NewLoadBalancerReconciler(lbs, lbBackends, computes, vpcs, manager.NewExecLB()),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := agent.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
