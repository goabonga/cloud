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
    { key: "note", label: "Note", type: "string", helpText: "Optional free-form note." },
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

  it("shows a Project picker for a scoped kind and sends it on create", async () => {
    vi.mocked(generic.listResources).mockImplementation((kind) =>
      Promise.resolve(kind === "project" ? [{ metadata: { uid: "project-1", generation: 1, createdAt: "" }, spec: {}, status: {} }] : []),
    );
    vi.mocked(generic.createResource).mockResolvedValue({
      metadata: { uid: "widget-1", generation: 1, createdAt: "" },
      spec: {},
      status: {},
    });

    renderForm();

    fireEvent.change(screen.getByLabelText("UID"), { target: { value: "widget-1" } });
    fireEvent.change(screen.getByLabelText("Name *"), { target: { value: "My widget" } });
    await waitFor(() => screen.getByLabelText("Project"));
    fireEvent.change(screen.getByLabelText("Project"), { target: { value: "project-1" } });

    fireEvent.click(screen.getByText("Create"));

    await waitFor(() =>
      expect(generic.createResource).toHaveBeenCalledWith(
        "widget",
        "widget-1",
        { name: "My widget" },
        { projectId: "project-1" },
      ),
    );
  });

  it("drops a group field that was added to and then emptied back out", async () => {
    vi.mocked(generic.createResource).mockResolvedValue({
      metadata: { uid: "widget-1", generation: 1, createdAt: "" },
      spec: {},
      status: {},
    });

    renderForm();

    fireEvent.change(screen.getByLabelText("UID"), { target: { value: "widget-1" } });
    fireEvent.change(screen.getByLabelText("Name *"), { target: { value: "My widget" } });

    fireEvent.click(screen.getByText("Add items"));
    fireEvent.click(screen.getByLabelText("Remove row 1"));
    fireEvent.click(screen.getByText("Create"));

    await waitFor(() => expect(generic.createResource).toHaveBeenCalledWith("widget", "widget-1", { name: "My widget" }));
  });

  it("creates an unscoped resource with exactly 3 arguments when no project is picked", async () => {
    vi.mocked(generic.createResource).mockResolvedValue({
      metadata: { uid: "widget-1", generation: 1, createdAt: "" },
      spec: {},
      status: {},
    });

    renderForm();

    fireEvent.change(screen.getByLabelText("UID"), { target: { value: "widget-1" } });
    fireEvent.change(screen.getByLabelText("Name *"), { target: { value: "My widget" } });
    fireEvent.click(screen.getByText("Create"));

    await waitFor(() => expect(generic.createResource).toHaveBeenCalledWith("widget", "widget-1", { name: "My widget" }));
  });

  it("hides the Project picker for a kind registered with scoped: false", () => {
    render(
      <MemoryRouter initialEntries={["/form"]}>
        <Routes>
          <Route path="/widget" element={<div>list page</div>} />
          <Route path="/form" element={<ResourceForm def={{ ...widgetDef, scoped: false }} />} />
        </Routes>
      </MemoryRouter>,
    );
    expect(screen.queryByLabelText("Project")).toBeNull();
  });

  it("hides the Project picker when editing, even for a scoped kind", async () => {
    vi.mocked(generic.getResource).mockResolvedValue({
      metadata: { uid: "widget-1", generation: 1, createdAt: "" },
      spec: { name: "Existing" },
      status: {},
    });

    renderForm("widget-1");

    await waitFor(() => screen.getByDisplayValue("Existing"));
    expect(screen.queryByLabelText("Project")).toBeNull();
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

  it("shows an error when the resource fails to load in edit mode", async () => {
    vi.mocked(generic.getResource).mockRejectedValue(new Error("boom"));

    renderForm("widget-1");

    await waitFor(() => screen.getByText("Could not load this resource."));
  });

  it("switches back to the guided form from Advanced mode, keeping valid edits", async () => {
    renderForm();
    fireEvent.change(screen.getByLabelText("UID"), { target: { value: "widget-1" } });
    fireEvent.change(screen.getByLabelText("Name *"), { target: { value: "first" } });

    fireEvent.click(screen.getByText("Advanced: edit raw JSON"));
    fireEvent.change(screen.getByLabelText("Spec (JSON)"), { target: { value: '{"name":"from-json"}' } });
    fireEvent.click(screen.getByText("Use the guided form"));

    await screen.findByDisplayValue("from-json");
  });

  it("leaves the structured values untouched when switching back with invalid JSON", async () => {
    renderForm();
    fireEvent.change(screen.getByLabelText("UID"), { target: { value: "widget-1" } });
    fireEvent.change(screen.getByLabelText("Name *"), { target: { value: "first" } });

    fireEvent.click(screen.getByText("Advanced: edit raw JSON"));
    fireEvent.change(screen.getByLabelText("Spec (JSON)"), { target: { value: "{not json" } });
    fireEvent.click(screen.getByText("Use the guided form"));

    await screen.findByDisplayValue("first");
  });

  it("shows an error when creation fails", async () => {
    vi.mocked(generic.createResource).mockRejectedValue(new Error("boom"));

    renderForm();
    fireEvent.change(screen.getByLabelText("UID"), { target: { value: "widget-1" } });
    fireEvent.change(screen.getByLabelText("Name *"), { target: { value: "My widget" } });
    fireEvent.click(screen.getByText("Create"));

    await waitFor(() => screen.getByText("Error: boom"));
  });

  it("navigates to the list page when Cancel is clicked", async () => {
    renderForm();

    fireEvent.click(screen.getByText("Cancel"));

    await waitFor(() => screen.getByText("list page"));
  });
  it("preserves project metadata and fields outside the guided schema when editing", async () => {
    vi.mocked(generic.getResource).mockResolvedValue({ metadata: { uid: "widget-1", generation: 2, createdAt: "", projectId: "project-1", name: "Display name" }, spec: { name: "Existing", extension: { keep: true } }, status: {} });
    vi.mocked(generic.createResource).mockResolvedValue({ metadata: { uid: "widget-1", generation: 3, createdAt: "" }, spec: {}, status: {} });
    renderForm("widget-1");
    await waitFor(() => expect((screen.getByLabelText("Name *") as HTMLInputElement).value).toBe("Existing"));
    fireEvent.change(screen.getByLabelText("Name *"), { target: { value: "Changed" } });
    fireEvent.click(screen.getByText("Save"));
    await waitFor(() => expect(generic.createResource).toHaveBeenCalledWith("widget", "widget-1", expect.objectContaining({ name: "Changed", extension: { keep: true } }), expect.objectContaining({ projectId: "project-1", name: "Display name" })));
  });

});
