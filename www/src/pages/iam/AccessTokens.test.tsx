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

  it("shows an error when the list fails to load", async () => {
    vi.mocked(idp.listAccessTokens).mockRejectedValue(new Error("boom"));
    render(<AccessTokens />);
    await waitFor(() => screen.getByText("Could not load access tokens."));
  });

  it("shows an error when creating a token fails", async () => {
    vi.mocked(idp.listAccessTokens).mockResolvedValue([]);
    vi.mocked(idp.putAccessToken).mockRejectedValue(new Error("boom"));
    render(<AccessTokens />);
    await waitFor(() => screen.getByLabelText("Name"));

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "ci" } });
    fireEvent.click(screen.getByRole("button", { name: /create token/i }));

    await waitFor(() => screen.getByText("Could not create the access token."));
  });

  it("sends an ISO expiry and reveals an empty string when the response carries no token", async () => {
    vi.mocked(idp.listAccessTokens).mockResolvedValue([]);
    vi.mocked(idp.putAccessToken).mockResolvedValue({ ...ciToken, token: undefined });
    render(<AccessTokens />);
    await waitFor(() => screen.getByLabelText("Name"));

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "ci" } });
    fireEvent.change(screen.getByLabelText("Expires (optional)"), { target: { value: "2026-12-31" } });
    fireEvent.click(screen.getByRole("button", { name: /create token/i }));

    await waitFor(() =>
      expect(idp.putAccessToken).toHaveBeenCalledWith(expect.any(String), {
        name: "ci",
        expiresAt: new Date("2026-12-31").toISOString(),
      }),
    );
    expect(screen.queryByText(/won't be shown again/i)).toBeNull();
  });

  it("copies the revealed token to the clipboard", async () => {
    const writeText = vi.fn();
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    vi.mocked(idp.listAccessTokens).mockResolvedValue([]);
    vi.mocked(idp.putAccessToken).mockResolvedValue({ ...ciToken, token: "infra_s3cr3t" });
    render(<AccessTokens />);
    await waitFor(() => screen.getByLabelText("Name"));

    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "ci" } });
    fireEvent.click(screen.getByRole("button", { name: /create token/i }));
    await waitFor(() => screen.getByText("infra_s3cr3t"));

    fireEvent.click(screen.getByRole("button", { name: /copy/i }));

    expect(writeText).toHaveBeenCalledWith("infra_s3cr3t");
  });

  it("skips revocation when the confirmation is declined", async () => {
    vi.mocked(idp.listAccessTokens).mockResolvedValue([ciToken]);
    vi.spyOn(window, "confirm").mockReturnValue(false);
    render(<AccessTokens />);
    await waitFor(() => screen.getByText("ci"));

    fireEvent.click(screen.getByRole("button", { name: /revoke/i }));

    expect(idp.deleteAccessToken).not.toHaveBeenCalled();
  });

  it("shows an error when revoking a token fails", async () => {
    vi.mocked(idp.listAccessTokens).mockResolvedValue([ciToken]);
    vi.mocked(idp.deleteAccessToken).mockRejectedValue(new Error("boom"));
    render(<AccessTokens />);
    await waitFor(() => screen.getByText("ci"));

    fireEvent.click(screen.getByRole("button", { name: /revoke/i }));

    await waitFor(() => screen.getByText("Could not revoke the access token."));
  });

  it("shows an expiry date for a token that has one", async () => {
    const expiringToken: idp.AccessToken = {
      ...ciToken,
      metadata: { uid: "tok-2", createdAt: "2026-01-01T00:00:00Z" },
      spec: { name: "temp", expiresAt: "2026-12-31T00:00:00Z" },
    };
    vi.mocked(idp.listAccessTokens).mockResolvedValue([expiringToken]);
    render(<AccessTokens />);

    await waitFor(() => screen.getByText("temp"));
    screen.getByText(new Date("2026-12-31T00:00:00Z").toLocaleDateString());
  });
});
