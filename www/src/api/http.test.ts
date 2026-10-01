// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { afterEach, describe, expect, it, vi } from "vitest";

import { setToken } from "../auth";
import { apiRequest } from "./http";

describe("apiRequest", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    setToken("");
  });

  it("omits the Authorization header when there is no stored token", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);

    await apiRequest("GET", "/vpc");

    const [, opts] = fetchMock.mock.calls[0];
    expect(opts.headers.Authorization).toBeUndefined();
  });

  it("attaches a bearer token when one is stored", async () => {
    setToken("a-token");
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);

    await apiRequest("GET", "/vpc");

    const [, opts] = fetchMock.mock.calls[0];
    expect(opts.headers.Authorization).toBe("Bearer a-token");
  });

  it("throws with the method, path and status on a non-2xx response", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: false, status: 404 });
    vi.stubGlobal("fetch", fetchMock);

    await expect(apiRequest("GET", "/vpc/missing")).rejects.toThrow("GET /vpc/missing: 404");
  });
});
