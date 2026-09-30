// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

/// <reference types="vitest/config" />
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// During `vite dev`, /api and /idp are proxied to the control plane and the
// identity provider so the SPA runs against real backends without CORS. In
// production the Go server (infra-www) performs the same proxying.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": "http://localhost:8080",
      "/idp": {
        target: "http://localhost:8081",
        rewrite: (path) => path.replace(/^\/idp/, ""),
      },
    },
  },
  test: {
    environment: "jsdom",
    globals: true,
  },
});
