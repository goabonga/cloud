// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as generic from "../api/generic";
import ResourceDetail from "./ResourceDetail";

vi.mock("../api/generic", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/generic")>();
  return { ...actual, getResource: vi.fn(), deleteResource: vi.fn() };
});

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/:kind" element={<div>list page</div>} />
        <Route path="/:kind/:uid" element={<ResourceDetail />} />
        <Route path="/:kind/:uid/edit" element={<div>edit page</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("ResourceDetail", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("shows metadata, spec and status for a known kind", async () => {
    vi.mocked(generic.getResource).mockResolvedValue({
      metadata: { uid: "subnet-1", generation: 3, createdAt: "2026-01-01T00:00:00Z" },
      spec: { vpcId: "vpc-1", cidr: "10.0.1.0/24", type: "public" },
      status: { phase: "Ready", gateway: "10.0.1.1" },
    });

    renderAt("/subnet/subnet-1");

    expect(generic.getResource).toHaveBeenCalledWith("subnet", "subnet-1");
    await waitFor(() => screen.getAllByText("subnet-1"));
    screen.getByText("Ready");
    screen.getByText("10.0.1.0/24");
    screen.getByText("10.0.1.1");
  });

  it("links a reference field to the resource it points at", async () => {
    vi.mocked(generic.getResource).mockResolvedValue({
      metadata: { uid: "subnet-1", generation: 1, createdAt: "" },
      spec: { vpcId: "vpc-1", cidr: "10.0.1.0/24" },
      status: {},
    });

    renderAt("/subnet/subnet-1");

    await waitFor(() => screen.getByText("vpc-1"));
    const link = screen.getByText("vpc-1").closest("a");
    expect(link?.getAttribute("href")).toBe("/vpc/vpc-1");
  });

  it("navigates to the edit page", async () => {
    vi.mocked(generic.getResource).mockResolvedValue({
      metadata: { uid: "vpc-1", generation: 1, createdAt: "" },
      spec: { cidr: "10.0.0.0/16" },
      status: {},
    });

    renderAt("/vpc/vpc-1");
    await waitFor(() => screen.getByText("10.0.0.0/16"));

    fireEvent.click(screen.getByText("Edit"));
    await waitFor(() => screen.getByText("edit page"));
  });

  it("deletes the resource and navigates back to the list after confirmation", async () => {
    vi.mocked(generic.getResource).mockResolvedValue({
      metadata: { uid: "vpc-1", generation: 1, createdAt: "" },
      spec: { cidr: "10.0.0.0/16" },
      status: {},
    });
    vi.mocked(generic.deleteResource).mockResolvedValue(undefined);
    vi.spyOn(window, "confirm").mockReturnValue(true);

    renderAt("/vpc/vpc-1");
    await waitFor(() => screen.getByText("10.0.0.0/16"));

    fireEvent.click(screen.getByText("Delete"));

    await waitFor(() => expect(generic.deleteResource).toHaveBeenCalledWith("vpc", "vpc-1"));
    await waitFor(() => screen.getByText("list page"));
  });

  it("shows the project and owner when the resource carries them", async () => {
    vi.mocked(generic.getResource).mockResolvedValue({
      metadata: { uid: "vpc-1", generation: 1, createdAt: "", projectId: "project-1", ownerUid: "alice" },
      spec: { cidr: "10.0.0.0/16" },
      status: {},
    });

    renderAt("/vpc/vpc-1");

    await waitFor(() => screen.getByText("Project"));
    const link = screen.getByText("project-1").closest("a");
    expect(link?.getAttribute("href")).toBe("/project/project-1");
    screen.getByText("Owner");
    screen.getByText("alice");
  });

  it("shows neither project nor owner when the resource carries neither", async () => {
    vi.mocked(generic.getResource).mockResolvedValue({
      metadata: { uid: "vpc-1", generation: 1, createdAt: "" },
      spec: { cidr: "10.0.0.0/16" },
      status: {},
    });

    renderAt("/vpc/vpc-1");

    await waitFor(() => screen.getByText("10.0.0.0/16"));
    expect(screen.queryByText("Project")).toBeNull();
    expect(screen.queryByText("Owner")).toBeNull();
  });

  it("shows an error for an unknown kind instead of crashing", () => {
    renderAt("/not-a-kind/x");

    screen.getByText('Unknown resource kind "not-a-kind".');
    expect(generic.getResource).not.toHaveBeenCalled();
  });
});
