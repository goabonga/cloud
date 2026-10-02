// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// Package integration holds the privileged, kernel-level integration suite.
// Tests live behind the `integration` build tag and require a Linux host with
// root, CAP_NET_ADMIN and cgroups (see PLAN.md, risk 1).
package integration
