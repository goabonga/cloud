// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package controllers

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
)

// VPCSummary is the cluster-wide count of VPCs by phase.
type VPCSummary struct {
	Total   int
	Ready   int
	Pending int
	Error   int
	Other   int
}

// VPCSummaryController is a cluster-level controller that tallies VPC phases.
// It is a small, read-only example of the controller framework; richer
// controllers (scheduling compute onto nodes) build on the same interface.
type VPCSummaryController struct {
	reg    *registry.Registry[resource.VPCSpec, resource.VPCStatus]
	logger *slog.Logger

	mu   sync.Mutex
	last VPCSummary
}

// NewVPCSummaryController returns a controller reading VPCs from reg.
func NewVPCSummaryController(reg *registry.Registry[resource.VPCSpec, resource.VPCStatus], logger *slog.Logger) *VPCSummaryController {
	if logger == nil {
		logger = slog.Default()
	}
	return &VPCSummaryController{reg: reg, logger: logger}
}

// Name identifies the controller.
func (c *VPCSummaryController) Name() string { return "vpc-summary" }

// Reconcile recomputes and records the VPC phase summary.
func (c *VPCSummaryController) Reconcile(ctx context.Context) error {
	vpcs, err := c.reg.WithContext(ctx).List()
	if err != nil {
		return fmt.Errorf("controllers: list vpcs: %w", err)
	}

	var s VPCSummary
	s.Total = len(vpcs)
	for i := range vpcs {
		switch vpcs[i].Status.Phase {
		case resource.PhaseReady:
			s.Ready++
		case resource.PhasePending, resource.PhaseReconciling, "":
			s.Pending++
		case resource.PhaseError:
			s.Error++
		default:
			s.Other++
		}
	}

	c.mu.Lock()
	c.last = s
	c.mu.Unlock()

	c.logger.InfoContext(ctx, "vpc summary",
		"total", s.Total, "ready", s.Ready, "pending", s.Pending, "error", s.Error)
	return nil
}

// Summary returns the most recently computed summary.
func (c *VPCSummaryController) Summary() VPCSummary {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}
