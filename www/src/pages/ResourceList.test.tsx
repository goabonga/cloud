// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as generic from "../api/generic";
import ResourceList from "./ResourceList";

vi.mock("../api/generic", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../api/generic")>();
  return { ...actual, listResources: vi.fn(), createResource: vi.fn(), deleteResource: vi.fn() };
});

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path=":kind" element={<ResourceList />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("ResourceList", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("renders the matching resource's table", async () => {
    vi.mocked(generic.listResources).mockResolvedValue([]);

    renderAt("/vpc");

    expect(generic.listResources).toHaveBeenCalledWith("vpc");
    await waitFor(() => screen.getByText("VPCs"));
  });

  it("shows an error for an unknown kind instead of crashing", () => {
    renderAt("/not-a-kind");

    screen.getByText('Unknown resource kind "not-a-kind".');
    expect(generic.listResources).not.toHaveBeenCalled();
  });
});
