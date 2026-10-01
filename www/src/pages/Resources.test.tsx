// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as generic from "../api/generic";
import Resources from "./Resources";

// A plain vi.mock() empties exported arrays (including KINDS, which the
// kind <select> renders its options from); importOriginal keeps it real
// while still mocking the API functions.
vi.mock("../api/generic", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/generic")>();
  return { ...actual, listResources: vi.fn(), createResource: vi.fn(), deleteResource: vi.fn(), getResource: vi.fn() };
});

const vpcA: generic.GenericResource = {
  metadata: { uid: "vpc-a", generation: 1, createdAt: "" },
  spec: { cidr: "10.0.0.0/16" },
  status: { phase: "Ready" },
};
const vpcB: generic.GenericResource = {
  metadata: { uid: "vpc-b", generation: 1, createdAt: "" },
  spec: { cidr: "10.1.0.0/16" },
  status: { phase: "Pending" },
};

function renderResources() {
  return render(
    <MemoryRouter>
      <Resources />
    </MemoryRouter>,
  );
}

describe("Resources", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("lists the selected kind with a link into the detail view", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([vpcA, vpcB]);
    renderResources();

    await waitFor(() => screen.getByText("vpc-a"));
    expect(generic.listResources).toHaveBeenCalledWith("vpc");
    const link = screen.getByText("vpc-a").closest("a");
    expect(link?.getAttribute("href")).toBe("/resources/vpc/vpc-a");
  });

  it("filters rows by the search query", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([vpcA, vpcB]);
    renderResources();
    await waitFor(() => screen.getByText("vpc-a"));

    fireEvent.change(screen.getByPlaceholderText(/search/i), { target: { value: "vpc-b" } });

    expect(screen.queryByText("vpc-a")).toBeNull();
    screen.getByText("vpc-b");
  });

  it("reloads with the new kind when the selector changes", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([]);
    renderResources();
    await waitFor(() => expect(generic.listResources).toHaveBeenCalledWith("vpc"));

    fireEvent.change(screen.getByLabelText("Kind"), { target: { value: "subnet" } });

    await waitFor(() => expect(generic.listResources).toHaveBeenCalledWith("subnet"));
  });

  it("creates a resource from the JSON form", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([]);
    vi.mocked(generic.createResource).mockResolvedValue(vpcA);
    renderResources();
    await waitFor(() => screen.getByLabelText("UID"));

    fireEvent.change(screen.getByLabelText("UID"), { target: { value: "vpc-a" } });
    fireEvent.change(screen.getByLabelText("Spec (JSON)"), { target: { value: '{"cidr":"10.0.0.0/16"}' } });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));

    await waitFor(() => expect(generic.createResource).toHaveBeenCalledWith("vpc", "vpc-a", { cidr: "10.0.0.0/16" }));
  });
});
