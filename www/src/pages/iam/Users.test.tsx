// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
const carolUser: idp.User = {
  metadata: { uid: "carol", createdAt: "2026-01-03T00:00:00Z" },
  spec: { username: "carol", disabled: true },
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

  it("shows an error when the list fails to load", async () => {
    vi.mocked(idp.listUsers).mockRejectedValue(new Error("boom"));
    render(<Users />);
    await waitFor(() => screen.getByText("Could not load users."));
  });

  it("shows an error when creating a user fails", async () => {
    vi.mocked(idp.listUsers).mockResolvedValue([]);
    vi.mocked(idp.putUser).mockRejectedValue(new Error("boom"));
    render(<Users />);
    await waitFor(() => screen.getByLabelText("Username"));

    fireEvent.change(screen.getByLabelText("Username"), { target: { value: "bob" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "s3cr3t" } });
    fireEvent.click(screen.getByRole("button", { name: /create user/i }));

    await waitFor(() => screen.getByText("Could not create the user."));
  });

  it("skips deletion when the confirmation is declined", async () => {
    vi.mocked(idp.listUsers).mockResolvedValue([aliceUser, bobUser]);
    vi.spyOn(window, "confirm").mockReturnValue(false);
    render(<Users />);
    await waitFor(() => screen.getByText("bob"));

    fireEvent.click(screen.getAllByRole("button", { name: /delete/i })[1]);

    expect(idp.deleteUser).not.toHaveBeenCalled();
  });

  it("shows an error when deleting a user fails", async () => {
    vi.mocked(idp.listUsers).mockResolvedValue([bobUser]);
    vi.mocked(idp.deleteUser).mockRejectedValue(new Error("boom"));
    render(<Users />);
    await waitFor(() => screen.getByText("bob"));

    fireEvent.click(screen.getByRole("button", { name: /delete/i }));

    await waitFor(() => screen.getByText("Could not delete the user."));
  });

  it("edits a user's roles, disabled status and password, then saves", async () => {
    vi.mocked(idp.listUsers).mockResolvedValue([bobUser]);
    vi.mocked(idp.putUser).mockResolvedValue(bobUser);
    render(<Users />);
    await waitFor(() => screen.getByText("bob"));

    fireEvent.click(screen.getByRole("button", { name: /edit/i }));

    const row = within(screen.getByText("bob").closest("tr")!);
    fireEvent.change(row.getByRole("textbox"), { target: { value: "admin, operator" } });
    fireEvent.click(row.getByRole("checkbox"));
    fireEvent.change(row.getByPlaceholderText("new password (optional)"), { target: { value: "newpass" } });
    fireEvent.click(row.getByRole("button", { name: /save/i }));

    await waitFor(() =>
      expect(idp.putUser).toHaveBeenCalledWith("bob", {
        username: "bob",
        roles: ["admin", "operator"],
        disabled: true,
        password: "newpass",
      }),
    );
  });

  it("cancels editing without saving", async () => {
    vi.mocked(idp.listUsers).mockResolvedValue([bobUser]);
    render(<Users />);
    await waitFor(() => screen.getByText("bob"));

    fireEvent.click(screen.getByRole("button", { name: /edit/i }));
    screen.getByRole("button", { name: /save/i });
    fireEvent.click(screen.getByRole("button", { name: /cancel/i }));

    expect(screen.queryByRole("button", { name: /save/i })).toBeNull();
    expect(idp.putUser).not.toHaveBeenCalled();
  });

  it("shows an error when saving an edit fails", async () => {
    vi.mocked(idp.listUsers).mockResolvedValue([bobUser]);
    vi.mocked(idp.putUser).mockRejectedValue(new Error("boom"));
    render(<Users />);
    await waitFor(() => screen.getByText("bob"));

    fireEvent.click(screen.getByRole("button", { name: /edit/i }));
    fireEvent.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() => screen.getByText("Could not update the user."));
  });

  it("shows 'none' for a user without roles and marks a disabled user", async () => {
    vi.mocked(idp.listUsers).mockResolvedValue([carolUser]);
    render(<Users />);

    await waitFor(() => screen.getByText("carol"));
    screen.getByText("none");
    screen.getByText("Disabled");
  });
});
