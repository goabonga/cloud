// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as generic from "../api/generic";
import { RESOURCES } from "../registry";
import Overview from "./Overview";

vi.mock("../api/generic", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/generic")>();
  return { ...actual, listResources: vi.fn() };
});

describe("Overview", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("requests every registered kind and groups tiles under their categories", async () => {
    vi.mocked(generic.listResources).mockImplementation((kind) =>
      Promise.resolve(kind === "vpc" ? [{ metadata: { uid: "vpc-1", generation: 1, createdAt: "" }, spec: {}, status: {} }] : []),
    );

    render(
      <MemoryRouter>
        <Overview />
      </MemoryRouter>,
    );

    expect(generic.listResources).toHaveBeenCalledTimes(Object.keys(RESOURCES).length);
    screen.getByText("VPC network");
    screen.getByText("Security");

    await waitFor(() => screen.getByText("1"));
    const vpcLink = screen.getByText("VPCs").closest("a");
    expect(vpcLink?.getAttribute("href")).toBe("/vpc");
  });

  it("shows an error when a kind fails to load", async () => {
    vi.mocked(generic.listResources).mockImplementation((kind) =>
      kind === "vpc" ? Promise.reject(new Error("boom")) : Promise.resolve([]),
    );

    render(
      <MemoryRouter>
        <Overview />
      </MemoryRouter>,
    );

    await waitFor(() => screen.getByText("Error: boom"));
  });
});
