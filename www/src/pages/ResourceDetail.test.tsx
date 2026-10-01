// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as generic from "../api/generic";
import ResourceDetail from "./ResourceDetail";

vi.mock("../api/generic");

const vpcResource: generic.GenericResource = {
  metadata: { uid: "vpc-1", generation: 2, createdAt: "2026-01-01T00:00:00Z" },
  spec: { cidr: "10.0.0.0/16" },
  status: { phase: "Ready", bridgeName: "br-vpc-1" },
};

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/resources/:kind/:uid" element={<ResourceDetail />} />
        <Route path="/vpcs" element={<div>vpcs list</div>} />
        <Route path="/resources" element={<div>resources list</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("ResourceDetail", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.spyOn(window, "confirm").mockReturnValue(true);
  });

  it("renders spec and status, with a VPCs breadcrumb for kind=vpc", async () => {
    vi.mocked(generic.getResource).mockResolvedValue(vpcResource);
    renderAt("/resources/vpc/vpc-1");

    await waitFor(() => screen.getByText("vpc-1"));
    screen.getByText("VPCs");
    screen.getByText("10.0.0.0/16");
    screen.getByText("br-vpc-1");
  });

  it("falls back to a Resources breadcrumb for an unknown kind", async () => {
    vi.mocked(generic.getResource).mockResolvedValue({
      ...vpcResource,
      metadata: { ...vpcResource.metadata, uid: "disk-1" },
    });
    renderAt("/resources/disk/disk-1");

    await waitFor(() => screen.getByText("disk-1"));
    screen.getByText("Resources");
  });

  it("deletes after confirming, then navigates back to the list", async () => {
    vi.mocked(generic.getResource).mockResolvedValue(vpcResource);
    vi.mocked(generic.deleteResource).mockResolvedValue(undefined);
    renderAt("/resources/vpc/vpc-1");

    await waitFor(() => screen.getByText("vpc-1"));
    fireEvent.click(screen.getByRole("button", { name: /delete/i }));

    await waitFor(() => screen.getByText("vpcs list"));
    expect(generic.deleteResource).toHaveBeenCalledWith("vpc", "vpc-1");
  });
});
