// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

package main

import "embed"

// distFS holds the built single-page app. `make build-www` populates dist/ from
// the Vite build; a committed .gitkeep keeps this directory present so the embed
// always compiles.
//
//go:embed all:dist
var distFS embed.FS
