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
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/goabonga/infrastructure/internal/crypto"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/manager"
	"github.com/goabonga/infrastructure/internal/meta"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/ssl"
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
	microvms := registry.New[resource.MicroVMSpec, resource.MicroVMStatus](store, resource.KindMicroVM)
	ipAddresses := registry.New[resource.IPAddressSpec, resource.IPAddressStatus](store, resource.KindIPAddress)
	diskFiles := registry.New[resource.DiskFileSpec, resource.DiskFileStatus](store, resource.KindDiskFile)
	sslCAs := registry.New[resource.SSLCASpec, resource.SSLCAStatus](store, resource.KindSSLCA)
	nodeID := os.Getenv("GOA_NODE_ID")
	lbReconciler := manager.NewLoadBalancerReconciler(lbs, lbBackends, computes, vpcs, manager.NewExecLB())
	if os.Getenv("GOA_PUBLIC_CIDR") != "" {
		// An edge: also serve each load balancer's public address.
		lbReconciler.AsEdge(ipAddresses)
	}
	diskFileReconciler := manager.NewDiskFileReconciler(diskFiles, computes, manager.FSDiskFileWriter{}, nodeID)
	if master != nil {
		// Certificate keys are sealed with the KMS key: rendering them takes it.
		kek, err := crypto.NewKEK(master)
		if err != nil {
			return err
		}
		sslCerts := registry.New[resource.SSLCertSpec, resource.SSLCertStatus](store, resource.KindSSLCert)
		diskFileReconciler.WithCertificates(manager.NewSSLCertificates(ssl.NewService(sslCAs, sslCerts, kek)))
	}
	dnsReconciler := manager.NewDNSReconciler(dnsZones, dnsRecords, vpcs, manager.NewNativeDNS()).WithPublicAddress(os.Getenv("GOA_DNS_PUBLIC_ADDR"))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	passes := []manager.ReconcilePass{
		manager.NewNodeHeartbeat(nodes, nodeID),
		manager.NewVPCReconciler(vpcs, net),
		manager.NewOverlayReconciler(vpcs, nodes, manager.NewExecOverlay(), nodeID),
		manager.NewSubnetReconciler(subnets, vpcs, net).WithNodeIdentity(nodes, nodeID),
		manager.NewIGWReconciler(igws, vpcs, net),
		manager.NewPeeringReconciler(peerings, vpcs, manager.NewExecPeering()),
		// GOA_DNS_PUBLIC_ADDR (set on the edges) answers the public zones.
		dnsReconciler,
		manager.NewDiskReconciler(disks, manager.NewExecDiskBackend(filepath.Join(*stateDir, "disks")), master),
		manager.NewSecurityGroupReconciler(sgs, sgRules, manager.NewExecSecurityGroup()),
		manager.NewACLReconciler(acls, manager.NewExecFirewall()),
		manager.NewComputeReconciler(computes, subnets, vpcs, disks, sgs, manager.NewExecComputeBackend(*stateDir), nodeID).WithAddressStore(store),
		manager.NewMicroVMReconciler(microvms, subnets, vpcs, sgs, manager.NewExecMicroVMBackend(*stateDir), nodeID).WithAddressStore(store),
		manager.NewTrustReconciler(sslCAs, computes, subnets, manager.FSTrustWriter{}, nodeID),
		diskFileReconciler,
		manager.NewWAFReconciler(wafPolicies, wafRules, computes, subnets, igws, vpcs, manager.NewExecWAF()),
		// GOA_PUBLIC_CIDR (set on the edges) is the public block routed to them.
		// The public DNS address is taken: never hand it to an ip_address.
		manager.NewPublicIPReconciler(ipAddresses, store, os.Getenv("GOA_PUBLIC_CIDR"), os.Getenv("GOA_DNS_PUBLIC_ADDR")),
		lbReconciler,
	}
	// GOA_BGP_ASN (set on the edges) announces what they serve to the
	// upstream, last, once the passes above have served it.
	speaker, err := bgpSpeaker(ctx, logger)
	if err != nil {
		return err
	}
	if speaker != nil {
		passes = append(passes, manager.NewBGPReconciler(speaker, dnsReconciler, lbReconciler))
		defer func() {
			// Withdraw at once rather than when the upstream's hold timer runs out.
			stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := speaker.Stop(stopCtx); err != nil {
				logger.Error("stop bgp", "err", err)
			}
		}()
	}
	agent := manager.NewAgent(*interval, logger, passes...)

	if err := agent.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// bgpSpeaker starts the BGP speaker GOA_BGP_* configures, or returns nil when
// GOA_BGP_ASN is unset.
func bgpSpeaker(ctx context.Context, logger *slog.Logger) (*manager.GoBGPSpeaker, error) {
	raw := os.Getenv("GOA_BGP_ASN")
	if raw == "" {
		return nil, nil
	}
	asn, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("GOA_BGP_ASN %q: %w", raw, err)
	}
	routerID, err := netip.ParseAddr(os.Getenv("GOA_BGP_ROUTER_ID"))
	if err != nil {
		return nil, fmt.Errorf("GOA_BGP_ROUTER_ID: %w", err)
	}
	peers, err := manager.ParseBGPPeers(os.Getenv("GOA_BGP_PEERS"))
	if err != nil {
		return nil, err
	}
	return manager.NewGoBGPSpeaker(ctx, manager.BGPConfig{
		ASN:      uint32(asn),
		RouterID: routerID,
		Peers:    peers,
		BFD:      envOr("GOA_BGP_BFD", "true") == "true",
	}, logger)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
