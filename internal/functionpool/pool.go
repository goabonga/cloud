// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package functionpool creates function-owned compute slots atomically.
package functionpool

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/goabonga/infrastructure/internal/domain/resource"
	"github.com/goabonga/infrastructure/internal/registry"
	"github.com/goabonga/infrastructure/internal/state"
)

// Label identifies computes owned by the function pool.
const Label = "infra.io/function-id"

// Computes is the registry used by warm pools and cold starts.
type Computes = registry.Registry[resource.ComputeSpec, resource.ComputeStatus]

// CreateCompute claims a host port by creating its canonical compute UID with
// CAS. Warm-pool and cold-start writers share this operation; no separate
// reservation can outlive the resource it protects.
func CreateCompute(computes *Computes, fn *resource.Function, now time.Time) (*resource.Compute, int, error) {
	effective := *fn
	effective.Spec = fn.Spec.WithDefaults()
	if err := effective.Spec.Validate(); err != nil {
		return nil, 0, err
	}
	fn = &effective
	existing, err := computes.List()
	if err != nil {
		return nil, 0, err
	}
	used := make(map[int]bool)
	for _, cp := range existing {
		for _, mapping := range cp.Spec.Ports {
			host, _, ok := strings.Cut(mapping, ":")
			if !ok {
				continue
			}
			if port, err := strconv.Atoi(host); err == nil {
				used[port] = true
			}
		}
	}
	for port := resource.FunctionPortRangeLo; port <= resource.FunctionPortRangeHi; port++ {
		if used[port] {
			continue
		}
		cp := &resource.Compute{
			Metadata: resource.ObjectMeta{UID: fmt.Sprintf("function-compute-%d", port), Name: fn.Spec.Name, Generation: 1,
				OwnerUID: fn.Metadata.OwnerUID, ProjectID: fn.Metadata.ProjectID, OrganizationID: fn.Metadata.OrganizationID,
				CreatedAt: now, Labels: map[string]string{Label: fn.Metadata.UID}},
			Spec: resource.ComputeSpec{SubnetID: fn.Spec.SubnetID, SecurityGroupID: fn.Spec.SecurityGroupID, NodePoolID: fn.Spec.NodePoolID,
				CPU: fn.Spec.CPU, MemoryMB: fn.Spec.MemoryMB, PidsMax: fn.Spec.PidsMax, Image: fn.Spec.Image, Command: fn.Spec.Command, Env: fn.Spec.Env,
				Ports: []string{fmt.Sprintf("%d:%d/tcp", port, fn.Spec.Port)}},
		}
		cp.Metadata.AddFinalizer(resource.ComputeFinalizer)
		if err := computes.Put(cp); errors.Is(err, state.ErrConflict) {
			continue
		} else if err != nil {
			return nil, 0, err
		}
		return cp, port, nil
	}
	return nil, 0, fmt.Errorf("no free function port in %d-%d", resource.FunctionPortRangeLo, resource.FunctionPortRangeHi)
}

// RetireCompute preserves cleanup finalizers after failed instance creation.
func RetireCompute(computes *Computes, uid string, now time.Time) error {
	for range 16 {
		ok, err := computes.TryUpdate(uid, func(cp *resource.Compute) error {
			cp.Metadata.DeletionTimestamp = &now
			cp.Metadata.AddFinalizer(resource.ComputeFinalizer)
			return nil
		})
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
	}
	return state.ErrConflict
}
