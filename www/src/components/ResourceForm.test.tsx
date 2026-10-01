// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Cpu } from "lucide-react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as generic from "../api/generic";
import type { ResourceDef } from "../registry";
import ResourceForm from "./ResourceForm";

vi.mock("../api/generic", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/generic")>();
  return { ...actual, listResources: vi.fn(), getResource: vi.fn(), createResource: vi.fn() };
});

const widgetDef: ResourceDef = {
  kind: "widget",
  label: "Widget",
  pluralLabel: "Widgets",
  category: "compute",
  icon: Cpu,
  listColumns: [],
  searchableFields: [],
  fields: [
    { key: "name", label: "Name", type: "string", required: true },
    { key: "mode", label: "Mode", type: "enum", enumValues: ["a", "b"] },
    { key: "labels", label: "Labels", type: "keyValue" },
    { key: "refId", label: "Ref", type: "reference", referenceKind: "vpc" },
    { key: "items", label: "Items", type: "group", fields: [{ key: "x", label: "X", type: "string" }] },
  ],
};

// renderForm mounts at /form, with /widget as a stand-in list page so a
// submit/cancel navigation to it is observable.
function renderForm(uid?: string) {
  return render(
    <MemoryRouter initialEntries={["/form"]}>
      <Routes>
        <Route path="/widget" element={<div>list page</div>} />
        <Route path="/form" element={<ResourceForm def={widgetDef} uid={uid} />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("ResourceForm", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.mocked(generic.listResources).mockResolvedValue([]);
  });

  it("renders a field per schema entry with required markers", () => {
    renderForm();

    screen.getByLabelText("UID");
    screen.getByLabelText("Name *");
    screen.getByLabelText("Mode");
    expect(screen.queryByLabelText("Mode *")).toBeNull();
  });

  it("assembles a nested spec from string/enum/keyValue/group fields and creates the resource", async () => {
    vi.mocked(generic.createResource).mockResolvedValue({
      metadata: { uid: "widget-1", generation: 1, createdAt: "" },
      spec: {},
      status: {},
    });

    renderForm();

    fireEvent.change(screen.getByLabelText("UID"), { target: { value: "widget-1" } });
    fireEvent.change(screen.getByLabelText("Name *"), { target: { value: "My widget" } });
    fireEvent.change(screen.getByLabelText("Mode"), { target: { value: "b" } });

    fireEvent.click(screen.getByText("Add entry"));
    fireEvent.change(screen.getByPlaceholderText("key"), { target: { value: "tier" } });
    fireEvent.change(screen.getByPlaceholderText("value"), { target: { value: "gold" } });

    fireEvent.click(screen.getByText("Add items"));
    fireEvent.change(screen.getByLabelText("X"), { target: { value: "x-value" } });

    fireEvent.click(screen.getByText("Create"));

    await waitFor(() =>
      expect(generic.createResource).toHaveBeenCalledWith("widget", "widget-1", {
        name: "My widget",
        mode: "b",
        labels: { tier: "gold" },
        items: [{ x: "x-value" }],
      }),
    );
    await waitFor(() => screen.getByText("list page"));
  });

  it("loads and pre-fills an existing resource in edit mode, keeping the UID read-only", async () => {
    vi.mocked(generic.getResource).mockResolvedValue({
      metadata: { uid: "widget-1", generation: 2, createdAt: "" },
      spec: { name: "Existing", mode: "a", labels: { env: "prod" } },
      status: {},
    });

    renderForm("widget-1");

    await waitFor(() => expect(generic.getResource).toHaveBeenCalledWith("widget", "widget-1"));
    await waitFor(() => screen.getByDisplayValue("Existing"));
    screen.getByText("widget-1");
    expect(screen.queryByLabelText("UID")).toBeNull();
    screen.getByDisplayValue("env");
    screen.getByDisplayValue("prod");
  });

  it("submits raw JSON when Advanced mode is used", async () => {
    vi.mocked(generic.createResource).mockResolvedValue({
      metadata: { uid: "widget-1", generation: 1, createdAt: "" },
      spec: {},
      status: {},
    });

    renderForm();
    fireEvent.change(screen.getByLabelText("UID"), { target: { value: "widget-1" } });

    fireEvent.click(screen.getByText("Advanced: edit raw JSON"));
    fireEvent.change(screen.getByLabelText("Spec (JSON)"), { target: { value: '{"custom":"value"}' } });
    fireEvent.click(screen.getByText("Create"));

    await waitFor(() => expect(generic.createResource).toHaveBeenCalledWith("widget", "widget-1", { custom: "value" }));
  });

  it("shows an error instead of submitting invalid Advanced JSON", async () => {
    renderForm();
    fireEvent.change(screen.getByLabelText("UID"), { target: { value: "widget-1" } });

    fireEvent.click(screen.getByText("Advanced: edit raw JSON"));
    fireEvent.change(screen.getByLabelText("Spec (JSON)"), { target: { value: "{not json" } });
    fireEvent.click(screen.getByText("Create"));

    await waitFor(() => screen.getByText("spec is not valid JSON"));
    expect(generic.createResource).not.toHaveBeenCalled();
  });
});
