// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package scheduler places workloads onto nodes. It is pure: it makes a
// decision from a demand and a snapshot of node capacity, leaving the actual
// binding to the caller. Strategies decide how to spend spare capacity.
package scheduler

import (
	"errors"
	"sort"
)

// ErrNoFit is returned when no node has room for the demand.
var ErrNoFit = errors.New("scheduler: no node fits the demand")

// Resources is an amount of compute capacity or demand.
type Resources struct {
	MilliCPU int64 `json:"milliCpu"`
	MemoryMB int64 `json:"memoryMb"`
}

// Node is a placement target with total capacity and what is already allocated.
type Node struct {
	Name      string    `json:"name"`
	Capacity  Resources `json:"capacity"`
	Allocated Resources `json:"allocated"`
}

// Free returns the unallocated capacity of the node.
func (n Node) Free() Resources {
	return Resources{
		MilliCPU: n.Capacity.MilliCPU - n.Allocated.MilliCPU,
		MemoryMB: n.Capacity.MemoryMB - n.Allocated.MemoryMB,
	}
}

// fits reports whether free capacity can satisfy demand.
func (r Resources) fits(demand Resources) bool {
	return r.MilliCPU >= demand.MilliCPU && r.MemoryMB >= demand.MemoryMB
}

// score is a single-number measure of free capacity used to rank nodes.
func (r Resources) score() int64 {
	return r.MilliCPU + r.MemoryMB
}

// Strategy ranks the nodes that fit a demand; the highest-ranked node wins.
type Strategy interface {
	// rank returns a comparable key for a node; the node with the largest key
	// is selected. Ties are broken by node name for determinism.
	rank(free Resources) int64
}

// BinPack favours the most-utilized node that still fits, packing workloads
// tightly so whole nodes can be freed.
type BinPack struct{}

func (BinPack) rank(free Resources) int64 { return -free.score() }

// Spread favours the least-utilized node, distributing workloads evenly.
type Spread struct{}

func (Spread) rank(free Resources) int64 { return free.score() }

// Schedule picks the name of the node that best fits demand under strategy, or
// ErrNoFit if none has room.
func Schedule(demand Resources, nodes []Node, strategy Strategy) (string, error) {
	fitting := make([]Node, 0, len(nodes))
	for _, n := range nodes {
		if n.Free().fits(demand) {
			fitting = append(fitting, n)
		}
	}
	if len(fitting) == 0 {
		return "", ErrNoFit
	}
	sort.Slice(fitting, func(i, j int) bool {
		ri, rj := strategy.rank(fitting[i].Free()), strategy.rank(fitting[j].Free())
		if ri != rj {
			return ri > rj
		}
		return fitting[i].Name < fitting[j].Name
	})
	return fitting[0].Name, nil
}
