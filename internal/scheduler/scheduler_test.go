// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package scheduler_test

import (
	"errors"
	"testing"

	"github.com/goabonga/infrastructure/internal/scheduler"
)

func nodes() []scheduler.Node {
	return []scheduler.Node{
		{Name: "a", Capacity: scheduler.Resources{MilliCPU: 4000, MemoryMB: 8000}, Allocated: scheduler.Resources{MilliCPU: 3000, MemoryMB: 6000}}, // free 1000/2000
		{Name: "b", Capacity: scheduler.Resources{MilliCPU: 4000, MemoryMB: 8000}, Allocated: scheduler.Resources{MilliCPU: 1000, MemoryMB: 2000}}, // free 3000/6000
	}
}

func TestScheduleBinPackPicksTightest(t *testing.T) {
	t.Parallel()

	got, err := scheduler.Schedule(scheduler.Resources{MilliCPU: 500, MemoryMB: 1000}, nodes(), scheduler.BinPack{})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if got != "a" {
		t.Fatalf("binpack picked %q, want a (least free that fits)", got)
	}
}

func TestScheduleSpreadPicksEmptiest(t *testing.T) {
	t.Parallel()

	got, err := scheduler.Schedule(scheduler.Resources{MilliCPU: 500, MemoryMB: 1000}, nodes(), scheduler.Spread{})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if got != "b" {
		t.Fatalf("spread picked %q, want b (most free)", got)
	}
}

func TestScheduleSkipsNodesThatDoNotFit(t *testing.T) {
	t.Parallel()

	// Demand fits only node b; binpack would prefer a but a is too small.
	got, err := scheduler.Schedule(scheduler.Resources{MilliCPU: 2000, MemoryMB: 4000}, nodes(), scheduler.BinPack{})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if got != "b" {
		t.Fatalf("picked %q, want b", got)
	}
}

func TestScheduleNoFit(t *testing.T) {
	t.Parallel()

	_, err := scheduler.Schedule(scheduler.Resources{MilliCPU: 99000, MemoryMB: 1}, nodes(), scheduler.Spread{})
	if !errors.Is(err, scheduler.ErrNoFit) {
		t.Fatalf("expected ErrNoFit, got %v", err)
	}
}

func TestScheduleTieBreaksByName(t *testing.T) {
	t.Parallel()

	even := []scheduler.Node{
		{Name: "z", Capacity: scheduler.Resources{MilliCPU: 1000, MemoryMB: 1000}},
		{Name: "a", Capacity: scheduler.Resources{MilliCPU: 1000, MemoryMB: 1000}},
	}
	got, err := scheduler.Schedule(scheduler.Resources{MilliCPU: 1, MemoryMB: 1}, even, scheduler.Spread{})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if got != "a" {
		t.Fatalf("tie should break to %q, got %q", "a", got)
	}
}
