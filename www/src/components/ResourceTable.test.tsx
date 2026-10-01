// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Network } from "lucide-react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as generic from "../api/generic";
import type { ResourceDef } from "../registry";
import ResourceTable from "./ResourceTable";

vi.mock("../api/generic", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/generic")>();
  return { ...actual, listResources: vi.fn(), createResource: vi.fn(), deleteResource: vi.fn() };
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

describe("ResourceTable", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("lists rows for the given kind", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([resource("vpc-a", "10.0.0.0/16"), resource("vpc-b", "10.1.0.0/16")]);

    render(<ResourceTable def={vpcDef} />);

    expect(generic.listResources).toHaveBeenCalledWith("vpc");
    await waitFor(() => screen.getByText("vpc-a"));
    screen.getByText("vpc-b");
    screen.getByText("10.0.0.0/16");
  });

  it("filters rows via the search box", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([resource("vpc-a", "10.0.0.0/16"), resource("vpc-b", "10.1.0.0/16")]);

    render(<ResourceTable def={vpcDef} />);
    await waitFor(() => screen.getByText("vpc-a"));

    fireEvent.change(screen.getByPlaceholderText("Search vpcs…"), { target: { value: "vpc-b" } });

    expect(screen.queryByText("vpc-a")).toBeNull();
    screen.getByText("vpc-b");
  });

  it("paginates beyond one page", async () => {
    const items = Array.from({ length: 12 }, (_, i) => resource(`vpc-${i}`, "10.0.0.0/16"));
    vi.mocked(generic.listResources).mockResolvedValue(items);

    render(<ResourceTable def={vpcDef} />);
    await waitFor(() => screen.getByText("vpc-0"));

    screen.getByText("Page 1 of 2");
    expect(screen.queryByText("vpc-11")).toBeNull();

    fireEvent.click(screen.getByText("Next"));
    await waitFor(() => screen.getByText("Page 2 of 2"));
    screen.getByText("vpc-11");
  });

  it("creates a resource from the UID + spec form", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([]);
    vi.mocked(generic.createResource).mockResolvedValue(resource("vpc-new", "10.2.0.0/16"));

    render(<ResourceTable def={vpcDef} />);
    await waitFor(() => expect(generic.listResources).toHaveBeenCalled());

    fireEvent.change(screen.getByLabelText("UID"), { target: { value: "vpc-new" } });
    fireEvent.change(screen.getByLabelText("Spec (JSON)"), { target: { value: '{"cidr":"10.2.0.0/16"}' } });
    fireEvent.click(screen.getByText("Create"));

    await waitFor(() => expect(generic.createResource).toHaveBeenCalledWith("vpc", "vpc-new", { cidr: "10.2.0.0/16" }));
  });

  it("deletes a resource after confirmation", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([resource("vpc-a", "10.0.0.0/16")]);
    vi.mocked(generic.deleteResource).mockResolvedValue(undefined);
    vi.spyOn(window, "confirm").mockReturnValue(true);

    render(<ResourceTable def={vpcDef} />);
    await waitFor(() => screen.getByText("vpc-a"));

    fireEvent.click(screen.getByText("Delete"));

    await waitFor(() => expect(generic.deleteResource).toHaveBeenCalledWith("vpc", "vpc-a"));
  });

  it("skips deletion when the confirmation is declined", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([resource("vpc-a", "10.0.0.0/16")]);
    vi.spyOn(window, "confirm").mockReturnValue(false);

    render(<ResourceTable def={vpcDef} />);
    await waitFor(() => screen.getByText("vpc-a"));

    fireEvent.click(screen.getByText("Delete"));

    expect(generic.deleteResource).not.toHaveBeenCalled();
  });
});
