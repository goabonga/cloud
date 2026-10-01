// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as client from "../api/client";
import Vpcs from "./Vpcs";

vi.mock("../api/client");

const vpcA: client.VPC = { metadata: { uid: "vpc-a", generation: 1, createdAt: "" }, spec: { cidr: "10.0.0.0/16" }, status: { phase: "Ready" } };
const vpcB: client.VPC = { metadata: { uid: "vpc-b", generation: 1, createdAt: "" }, spec: { cidr: "10.1.0.0/16" }, status: { phase: "Pending" } };

function renderVpcs() {
  return render(
    <MemoryRouter>
      <Vpcs />
    </MemoryRouter>,
  );
}

describe("Vpcs", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("lists VPCs with a link into the detail view", async () => {
    vi.mocked(client.listVPCs).mockResolvedValue([vpcA, vpcB]);
    renderVpcs();

    await waitFor(() => screen.getByText("vpc-a"));
    const link = screen.getByText("vpc-a").closest("a");
    expect(link?.getAttribute("href")).toBe("/resources/vpc/vpc-a");
  });

  it("filters rows by the search query", async () => {
    vi.mocked(client.listVPCs).mockResolvedValue([vpcA, vpcB]);
    renderVpcs();
    await waitFor(() => screen.getByText("vpc-a"));

    fireEvent.change(screen.getByPlaceholderText(/search/i), { target: { value: "10.1" } });

    expect(screen.queryByText("vpc-a")).toBeNull();
    screen.getByText("vpc-b");
  });

  it("toggles sort order on the UID column", async () => {
    vi.mocked(client.listVPCs).mockResolvedValue([vpcB, vpcA]);
    renderVpcs();
    await waitFor(() => screen.getByText("vpc-a"));

    const uidHeader = screen.getByRole("button", { name: "UID" });
    fireEvent.click(uidHeader);
    let rows = screen.getAllByRole("row").slice(1);
    expect(rows[0].textContent).toContain("vpc-a");

    fireEvent.click(uidHeader);
    rows = screen.getAllByRole("row").slice(1);
    expect(rows[0].textContent).toContain("vpc-b");
  });

  it("creates a VPC from the form", async () => {
    vi.mocked(client.listVPCs).mockResolvedValue([]);
    vi.mocked(client.createVPC).mockResolvedValue(vpcA);
    renderVpcs();
    await waitFor(() => screen.getByLabelText("UID"));

    fireEvent.change(screen.getByLabelText("UID"), { target: { value: "vpc-a" } });
    fireEvent.change(screen.getByLabelText("CIDR"), { target: { value: "10.0.0.0/16" } });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));

    await waitFor(() => expect(client.createVPC).toHaveBeenCalledWith("vpc-a", { cidr: "10.0.0.0/16" }));
  });
});
