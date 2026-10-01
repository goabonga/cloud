// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as idp from "../../api/idp";
import * as authContext from "../../auth-context";
import Users from "./Users";

vi.mock("../../api/idp");
vi.mock("../../auth-context");

const aliceUser: idp.User = {
  metadata: { uid: "alice", createdAt: "2026-01-01T00:00:00Z" },
  spec: { username: "alice", roles: ["admin"] },
};
const bobUser: idp.User = {
  metadata: { uid: "bob", createdAt: "2026-01-02T00:00:00Z" },
  spec: { username: "bob", roles: [] },
};

describe("Users", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.mocked(authContext.useAuth).mockReturnValue({ subject: "alice", roles: ["admin"], loading: false });
    vi.spyOn(window, "confirm").mockReturnValue(true);
  });

  it("shows a forbidden message for a non-admin", () => {
    vi.mocked(authContext.useAuth).mockReturnValue({ subject: "bob", roles: [], loading: false });
    render(<Users />);
    screen.getByText(/need the admin role/i);
  });

  it("lists users", async () => {
    vi.mocked(idp.listUsers).mockResolvedValue([aliceUser, bobUser]);
    render(<Users />);
    await waitFor(() => screen.getByText("alice"));
    screen.getByText("bob");
  });

  it("creates a user from the form", async () => {
    vi.mocked(idp.listUsers).mockResolvedValue([]);
    vi.mocked(idp.putUser).mockResolvedValue(bobUser);
    render(<Users />);
    await waitFor(() => screen.getByLabelText("Username"));

    fireEvent.change(screen.getByLabelText("Username"), { target: { value: "bob" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "s3cr3t" } });
    fireEvent.change(screen.getByLabelText("Roles (comma-separated)"), { target: { value: "operator, viewer" } });
    fireEvent.click(screen.getByRole("button", { name: /create user/i }));

    await waitFor(() =>
      expect(idp.putUser).toHaveBeenCalledWith("bob", { username: "bob", password: "s3cr3t", roles: ["operator", "viewer"] }),
    );
  });

  it("disables deleting your own row", async () => {
    vi.mocked(idp.listUsers).mockResolvedValue([aliceUser]);
    render(<Users />);
    await waitFor(() => screen.getByText("alice"));
    const deleteButtons = screen.getAllByRole("button", { name: /delete/i }) as HTMLButtonElement[];
    expect(deleteButtons[0].disabled).toBe(true);
  });

  it("deletes another user after confirming", async () => {
    vi.mocked(idp.listUsers).mockResolvedValue([aliceUser, bobUser]);
    vi.mocked(idp.deleteUser).mockResolvedValue(undefined);
    render(<Users />);
    await waitFor(() => screen.getByText("bob"));

    const deleteButtons = screen.getAllByRole("button", { name: /delete/i });
    fireEvent.click(deleteButtons[1]);

    await waitFor(() => expect(idp.deleteUser).toHaveBeenCalledWith("bob"));
  });
});
