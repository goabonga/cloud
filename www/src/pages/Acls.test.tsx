// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as client from "../api/client";
import Acls from "./Acls";

vi.mock("../api/client");

const web: client.ACLPolicy = {
  metadata: { uid: "web", generation: 1, createdAt: "" },
  spec: { rules: [{ action: "allow", protocol: "tcp", port: 443 }] },
  status: { phase: "Ready" },
};
const ssh: client.ACLPolicy = {
  metadata: { uid: "ssh", generation: 1, createdAt: "" },
  spec: { rules: [{ action: "allow", protocol: "tcp", port: 22 }] },
  status: { phase: "Pending" },
};

function renderAcls() {
  return render(
    <MemoryRouter>
      <Acls />
    </MemoryRouter>,
  );
}

describe("Acls", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("lists policies with a link into the detail view", async () => {
    vi.mocked(client.listACLs).mockResolvedValue([web, ssh]);
    renderAcls();

    await waitFor(() => screen.getByText("web"));
    const link = screen.getByText("web").closest("a");
    expect(link?.getAttribute("href")).toBe("/resources/acl_policy/web");
  });

  it("filters rows by the search query", async () => {
    vi.mocked(client.listACLs).mockResolvedValue([web, ssh]);
    renderAcls();
    await waitFor(() => screen.getByText("web"));

    fireEvent.change(screen.getByPlaceholderText(/search/i), { target: { value: "ssh" } });

    expect(screen.queryByText("web")).toBeNull();
    screen.getByText("ssh");
  });

  it("creates a policy from the form", async () => {
    vi.mocked(client.listACLs).mockResolvedValue([]);
    vi.mocked(client.createACL).mockResolvedValue(web);
    renderAcls();
    await waitFor(() => screen.getByLabelText("UID"));

    fireEvent.change(screen.getByLabelText("UID"), { target: { value: "web" } });
    fireEvent.click(screen.getByRole("button", { name: /create/i }));

    await waitFor(() =>
      expect(client.createACL).toHaveBeenCalledWith("web", { rules: [{ action: "allow", protocol: "tcp", port: 443 }] }),
    );
  });
});
