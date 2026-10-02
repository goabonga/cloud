// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// defaultCgroupBase is the cgroup v2 mount point.
const defaultCgroupBase = "/sys/fs/cgroup"

// cgroupManager creates cgroup v2 groups for compute instances under
// base/infra/<uid> and applies CPU, memory and PID limits. The write modes are
// irrelevant for the kernel pseudo-files (they already exist) and kept tight to
// satisfy the linters.
type cgroupManager struct {
	base string
}

// newCgroupManager rooted at base, defaulting to /sys/fs/cgroup.
func newCgroupManager(base string) *cgroupManager {
	if base == "" {
		base = defaultCgroupBase
	}
	return &cgroupManager{base: base}
}

// path returns the cgroup directory for a compute UID.
func (c *cgroupManager) path(uid string) string {
	return filepath.Join(c.base, "infra", uid)
}

// ensureParent creates base/infra and enables the cpu, memory and pids
// controllers so child groups can use them.
func (c *cgroupManager) ensureParent() error {
	parent := filepath.Join(c.base, "infra")
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return fmt.Errorf("manager: cgroup parent: %w", err)
	}
	_ = os.WriteFile(filepath.Join(c.base, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0o600)
	if err := os.WriteFile(filepath.Join(parent, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0o600); err != nil {
		return fmt.Errorf("manager: enable cgroup controllers: %w", err)
	}
	return nil
}

// setup creates the compute's cgroup and writes its resource limits. A cpu of 1
// maps to one core, memoryMB to a hard memory cap, pidsMax to a PID cap.
func (c *cgroupManager) setup(uid string, cpu float64, memoryMB, pidsMax int) error {
	if err := c.ensureParent(); err != nil {
		return err
	}
	dir := c.path(uid)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("manager: cgroup %q: %w", uid, err)
	}
	if cpu > 0 {
		quota := int(cpu * 100000)
		if err := os.WriteFile(filepath.Join(dir, "cpu.max"), []byte(fmt.Sprintf("%d 100000", quota)), 0o600); err != nil {
			return fmt.Errorf("manager: cpu.max %q: %w", uid, err)
		}
	}
	if memoryMB > 0 {
		bytes := memoryMB * 1024 * 1024
		if err := os.WriteFile(filepath.Join(dir, "memory.max"), []byte(strconv.Itoa(bytes)), 0o600); err != nil {
			return fmt.Errorf("manager: memory.max %q: %w", uid, err)
		}
		_ = os.WriteFile(filepath.Join(dir, "memory.swap.max"), []byte("0"), 0o600)
	}
	if pidsMax > 0 {
		if err := os.WriteFile(filepath.Join(dir, "pids.max"), []byte(strconv.Itoa(pidsMax)), 0o600); err != nil {
			return fmt.Errorf("manager: pids.max %q: %w", uid, err)
		}
	}
	return nil
}

// remove deletes the compute's cgroup directory. It fails while processes remain.
func (c *cgroupManager) remove(uid string) error {
	return os.Remove(c.path(uid))
}

// procsFile returns the cgroup.procs path for a compute UID.
func (c *cgroupManager) procsFile(uid string) string {
	return filepath.Join(c.path(uid), "cgroup.procs")
}
