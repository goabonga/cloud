// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { Network } from "lucide-react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as generic from "../api/generic";
import type { ResourceDef } from "../registry";
import ResourceTable from "./ResourceTable";

vi.mock("../api/generic", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/generic")>();
  return { ...actual, listResources: vi.fn(), deleteResource: vi.fn() };
});

const vpcDef: ResourceDef = {
  kind: "vpc",
  label: "VPC",
  pluralLabel: "VPCs",
  category: "networking",
  icon: Network,
  listColumns: [{ key: "spec.cidr", label: "CIDR" }],
  searchableFields: ["spec.cidr"],
  fields: [{ key: "cidr", label: "CIDR", type: "string", required: true }],
};

function resource(uid: string, cidr: string, phase = "Ready"): generic.GenericResource {
  return { metadata: { uid, generation: 1, createdAt: "" }, spec: { cidr }, status: { phase } };
}

function renderTable() {
  return render(
    <MemoryRouter initialEntries={["/vpc"]}>
      <Routes>
        <Route path="/:kind" element={<ResourceTable def={vpcDef} />} />
        <Route path="/:kind/new" element={<div>create page</div>} />
        <Route path="/:kind/:uid/edit" element={<div>edit page</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("ResourceTable", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("lists rows for the given kind", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([resource("vpc-a", "10.0.0.0/16"), resource("vpc-b", "10.1.0.0/16")]);

    renderTable();

    expect(generic.listResources).toHaveBeenCalledWith("vpc");
    await waitFor(() => screen.getByText("vpc-a"));
    screen.getByText("vpc-b");
    screen.getByText("10.0.0.0/16");
  });

  it("filters rows via the search box, and shows them all again once cleared", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([resource("vpc-a", "10.0.0.0/16"), resource("vpc-b", "10.1.0.0/16")]);

    renderTable();
    await waitFor(() => screen.getByText("vpc-a"));

    const search = screen.getByPlaceholderText("Search vpcs…");
    fireEvent.change(search, { target: { value: "vpc-b" } });

    expect(screen.queryByText("vpc-a")).toBeNull();
    screen.getByText("vpc-b");

    fireEvent.change(search, { target: { value: "   " } });

    screen.getByText("vpc-a");
    screen.getByText("vpc-b");
  });

  it("paginates beyond one page", async () => {
    const items = Array.from({ length: 12 }, (_, i) => resource(`vpc-${i}`, "10.0.0.0/16"));
    vi.mocked(generic.listResources).mockResolvedValue(items);

    renderTable();
    await waitFor(() => screen.getByText("vpc-0"));

    screen.getByText("Page 1 of 2");
    expect(screen.queryByText("vpc-11")).toBeNull();

    fireEvent.click(screen.getByText("Next"));
    await waitFor(() => screen.getByText("Page 2 of 2"));
    screen.getByText("vpc-11");
  });

  it("navigates to the create page", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([]);

    renderTable();
    await waitFor(() => expect(generic.listResources).toHaveBeenCalled());

    fireEvent.click(screen.getByText("Create vpc"));

    await waitFor(() => screen.getByText("create page"));
  });

  it("navigates to the edit page for a row", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([resource("vpc-a", "10.0.0.0/16")]);

    renderTable();
    await waitFor(() => screen.getByText("vpc-a"));

    fireEvent.click(screen.getByText("Edit"));

    await waitFor(() => screen.getByText("edit page"));
  });

  it("deletes a resource after confirmation", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([resource("vpc-a", "10.0.0.0/16")]);
    vi.mocked(generic.deleteResource).mockResolvedValue(undefined);
    vi.spyOn(window, "confirm").mockReturnValue(true);

    renderTable();
    await waitFor(() => screen.getByText("vpc-a"));

    fireEvent.click(screen.getByText("Delete"));

    await waitFor(() => expect(generic.deleteResource).toHaveBeenCalledWith("vpc", "vpc-a"));
  });

  it("skips deletion when the confirmation is declined", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([resource("vpc-a", "10.0.0.0/16")]);
    vi.spyOn(window, "confirm").mockReturnValue(false);

    renderTable();
    await waitFor(() => screen.getByText("vpc-a"));

    fireEvent.click(screen.getByText("Delete"));

    expect(generic.deleteResource).not.toHaveBeenCalled();
  });

  it("shows an error when the list fails to load", async () => {
    vi.mocked(generic.listResources).mockRejectedValue(new Error("boom"));

    renderTable();

    await waitFor(() => screen.getByText("Error: boom"));
  });

  it("shows an error when deletion fails", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([resource("vpc-a", "10.0.0.0/16")]);
    vi.mocked(generic.deleteResource).mockRejectedValue(new Error("boom"));
    vi.spyOn(window, "confirm").mockReturnValue(true);

    renderTable();
    await waitFor(() => screen.getByText("vpc-a"));

    fireEvent.click(screen.getByText("Delete"));

    await waitFor(() => screen.getByText("Error: boom"));
  });

  it("falls back to no phase badge text when a resource has none", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([
      { metadata: { uid: "vpc-a", generation: 1, createdAt: "" }, spec: { cidr: "10.0.0.0/16" }, status: {} },
    ]);

    renderTable();

    await waitFor(() => screen.getByText("vpc-a"));
    screen.getByText("-");
  });

  it("changes the page size", async () => {
    const items = Array.from({ length: 12 }, (_, i) => resource(`vpc-${i}`, "10.0.0.0/16"));
    vi.mocked(generic.listResources).mockResolvedValue(items);

    renderTable();
    await waitFor(() => screen.getByText("vpc-0"));
    screen.getByText("Page 1 of 2");

    fireEvent.change(screen.getByLabelText("Rows per page"), { target: { value: "25" } });

    await waitFor(() => screen.getByText("Page 1 of 1"));
    screen.getByText("vpc-11");
  });

  it("navigates back to the previous page", async () => {
    const items = Array.from({ length: 12 }, (_, i) => resource(`vpc-${i}`, "10.0.0.0/16"));
    vi.mocked(generic.listResources).mockResolvedValue(items);

    renderTable();
    await waitFor(() => screen.getByText("vpc-0"));

    fireEvent.click(screen.getByText("Next"));
    await waitFor(() => screen.getByText("Page 2 of 2"));

    fireEvent.click(screen.getByText("Previous"));

    await waitFor(() => screen.getByText("Page 1 of 2"));
    screen.getByText("vpc-0");
  });

  it("sorts rows by a column, toggling ascending and descending", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([resource("vpc-b", "10.1.0.0/16"), resource("vpc-a", "10.0.0.0/16")]);

    renderTable();
    await waitFor(() => screen.getByText("vpc-b"));

    const header = screen.getByText("UID");
    fireEvent.click(header);
    let rows = screen.getAllByRole("row").slice(1);
    expect(within(rows[0]).queryByText("vpc-a")).not.toBeNull();

    fireEvent.click(header);
    rows = screen.getAllByRole("row").slice(1);
    expect(within(rows[0]).queryByText("vpc-b")).not.toBeNull();
  });
});
