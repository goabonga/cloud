// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as generic from "../../api/generic";
import type { FieldSchema } from "../../registry/types";
import ReferenceField from "./ReferenceField";

vi.mock("../../api/generic", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../api/generic")>();
  return { ...actual, listResources: vi.fn() };
});

const subnetField: FieldSchema = { key: "subnetId", label: "Subnet", type: "reference", referenceKind: "subnet" };

describe("ReferenceField", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("populates its options from the referenced kind", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([
      { metadata: { uid: "sn-1", generation: 1, createdAt: "" }, spec: {}, status: {} },
      { metadata: { uid: "sn-2", name: "public", generation: 1, createdAt: "" }, spec: {}, status: {} },
    ]);

    render(<ReferenceField id="f" field={subnetField} value="" onChange={vi.fn()} />);

    expect(generic.listResources).toHaveBeenCalledWith("subnet");
    await waitFor(() => screen.getByText("sn-1"));
    screen.getByText("sn-2 (public)");
  });

  it("reports the selected uid on change", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([
      { metadata: { uid: "sn-1", generation: 1, createdAt: "" }, spec: {}, status: {} },
    ]);
    const onChange = vi.fn();

    render(<ReferenceField id="f" field={subnetField} value="" onChange={onChange} />);
    await waitFor(() => screen.getByText("sn-1"));

    fireEvent.change(screen.getByRole("combobox"), { target: { value: "sn-1" } });
    expect(onChange).toHaveBeenCalledWith("sn-1");
  });

  it("shows an error if the options fail to load", async () => {
    vi.mocked(generic.listResources).mockRejectedValue(new Error("boom"));

    render(<ReferenceField id="f" field={subnetField} value="" onChange={vi.fn()} />);

    await waitFor(() => screen.getByText("Could not load subnet options."));
  });
});
