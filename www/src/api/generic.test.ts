// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { afterEach, describe, expect, it, vi } from "vitest";

import { createResource, deleteResource, getResource, listResources } from "./generic";

describe("generic api", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
  });

  it("lists resources of a kind", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ items: [{ metadata: { uid: "sn-1", generation: 1, createdAt: "" }, spec: { cidr: "10.0.1.0/24" }, status: {} }] }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const items = await listResources("subnet");
    expect(items).toHaveLength(1);
    expect(items[0].metadata.uid).toBe("sn-1");
    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/subnet");
  });

  it("falls back to an empty array when the response carries no items", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({}) });
    vi.stubGlobal("fetch", fetchMock);

    const items = await listResources("subnet");
    expect(items).toEqual([]);
  });

  it("fetches a single resource by kind and uid", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ metadata: { uid: "sn-1", generation: 1, createdAt: "" }, spec: { cidr: "10.0.1.0/24" }, status: {} }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const item = await getResource("subnet", "sn-1");
    expect(item.metadata.uid).toBe("sn-1");
    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/subnet/sn-1");
  });

  it("sends the spec on create", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ metadata: { uid: "c-1", generation: 1, createdAt: "" }, spec: {}, status: {} }),
    });
    vi.stubGlobal("fetch", fetchMock);

    await createResource("compute", "c-1", { subnetId: "sn-1", image: "nginx:latest" });
    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/v1/compute/c-1");
    expect(opts.method).toBe("PUT");
    expect(JSON.parse(opts.body as string)).toEqual({ spec: { subnetId: "sn-1", image: "nginx:latest" } });
  });

  it("sends metadata.projectId on create when given", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 201,
      json: async () => ({ metadata: { uid: "v-1", generation: 1, createdAt: "" }, spec: {}, status: {} }),
    });
    vi.stubGlobal("fetch", fetchMock);

    await createResource("vpc", "v-1", { cidr: "10.0.0.0/16" }, { projectId: "project-1" });
    const [, opts] = fetchMock.mock.calls[0];
    expect(JSON.parse(opts.body as string)).toEqual({
      spec: { cidr: "10.0.0.0/16" },
      metadata: { projectId: "project-1" },
    });
  });

  it("deletes a resource", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 204 });
    vi.stubGlobal("fetch", fetchMock);
    await deleteResource("disk", "d-1");
    expect(fetchMock.mock.calls[0][1].method).toBe("DELETE");
  });
});
