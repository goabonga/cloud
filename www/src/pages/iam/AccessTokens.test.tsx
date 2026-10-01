// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as idp from "../../api/idp";
import * as authContext from "../../auth-context";
import AccessTokens from "./AccessTokens";

vi.mock("../../api/idp");
vi.mock("../../auth-context");

const ciToken: idp.AccessToken = {
  metadata: { uid: "tok-1", createdAt: "2026-01-01T00:00:00Z" },
  spec: { name: "ci" },
  status: { ownerUid: "alice" },
};

describe("AccessTokens", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.mocked(authContext.useAuth).mockReturnValue({ subject: "alice", roles: ["admin"], loading: false });
    vi.spyOn(window, "confirm").mockReturnValue(true);
  });

  it("shows a forbidden message for a non-admin", () => {
    vi.mocked(authContext.useAuth).mockReturnValue({ subject: "bob", roles: [], loading: false });
    render(<AccessTokens />);
    screen.getByText(/need the admin role/i);
  });

  it("lists tokens", async () => {
    vi.mocked(idp.listAccessTokens).mockResolvedValue([ciToken]);
    render(<AccessTokens />);
    await waitFor(() => screen.getByText("ci"));
    screen.getByText("alice");
  });

  it("creates a token and reveals the plaintext once", async () => {
    vi.mocked(idp.listAccessTokens).mockResolvedValue([]);
    vi.mocked(idp.putAccessToken).mockResolvedValue({ ...ciToken, token: "infra_s3cr3t" });
    render(<AccessTokens />);
    await waitFor(() => screen.getByLabelText("Name"));

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "ci" } });
    fireEvent.click(screen.getByRole("button", { name: /create token/i }));

    await waitFor(() => screen.getByText("infra_s3cr3t"));
    expect(idp.putAccessToken).toHaveBeenCalledWith(expect.any(String), { name: "ci", expiresAt: undefined });

    fireEvent.click(screen.getByRole("button", { name: /dismiss/i }));
    expect(screen.queryByText("infra_s3cr3t")).toBeNull();
  });

  it("revokes a token after confirming", async () => {
    vi.mocked(idp.listAccessTokens).mockResolvedValue([ciToken]);
    vi.mocked(idp.deleteAccessToken).mockResolvedValue(undefined);
    render(<AccessTokens />);
    await waitFor(() => screen.getByText("ci"));

    fireEvent.click(screen.getByRole("button", { name: /revoke/i }));

    await waitFor(() => expect(idp.deleteAccessToken).toHaveBeenCalledWith("tok-1"));
  });
});
