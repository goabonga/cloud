// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Command infra-controller-manager runs the cluster-level reconcile loops under
// leader election, so a single instance is active across the cluster.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/goabonga/infrastructure/internal/controllers"
	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/meta"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

func main() {
	if err := run(); err != nil {
		slog.Error("infra-controller-manager stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	stateDir := flag.String("state-dir", envOr("GOA_STATE_DIR", "./state"), "state directory")
	stateDSN := flag.String("state-dsn", envOr("GOA_STATE_DSN", ""), "PostgreSQL DSN (enables the HA backend)")
	interval := flag.Duration("interval", 5*time.Second, "reconcile interval")
	ttl := flag.Duration("lease-ttl", 15*time.Second, "leader lease TTL")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	logger.Info(meta.Line("infra-controller-manager", Version), "stateDir", *stateDir, "interval", interval.String())

	store, err := state.Open(*stateDir, *stateDSN)
	if err != nil {
		return err
	}
	vpcs := registry.New[resource.VPCSpec, resource.VPCStatus](store, resource.KindVPC)
	subnets := registry.New[resource.SubnetSpec, resource.SubnetStatus](store, resource.KindSubnet)
	sgs := registry.New[resource.SecurityGroupSpec, resource.SecurityGroupStatus](store, resource.KindSecurityGroup)
	computes := registry.New[resource.ComputeSpec, resource.ComputeStatus](store, resource.KindCompute)
	microvms := registry.New[resource.MicroVMSpec, resource.MicroVMStatus](store, resource.KindMicroVM)
	nodes := registry.New[resource.NodeSpec, resource.NodeStatus](store, resource.KindNode)
	nodePools := registry.New[resource.NodePoolSpec, resource.NodePoolStatus](store, resource.KindNodePool)
	functions := registry.New[resource.FunctionSpec, resource.FunctionStatus](store, resource.KindFunction)
	functionInstances := registry.New[resource.FunctionInstanceSpec, resource.FunctionInstanceStatus](store, resource.KindFunctionInstance)

	lease := controllers.NewLease(store, "leases/controller-manager", holderID(), *ttl, nil)
	mgr := controllers.NewManager(lease, *interval, logger)
	mgr.Add(controllers.NewVPCSummaryController(vpcs, logger))
	mgr.Add(controllers.NewSchedulerController(computes, nodes, nodePools, 4*(*interval), logger))
	mgr.Add(controllers.NewMicroVMSchedulerController(microvms, nodes, nodePools, 4*(*interval), logger))
	mgr.Add(controllers.NewFunctionController(functions, functionInstances, computes, subnets, vpcs, sgs, nil, logger))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := mgr.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// holderID is a per-process identity for the leader lease.
func holderID() string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
