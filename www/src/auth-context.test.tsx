// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as idp from "./api/idp";
import { AuthProvider, useAuth } from "./auth-context";

vi.mock("./api/idp");

function Probe() {
  const { subject, roles, loading } = useAuth();
  if (loading) {
    return <div>loading</div>;
  }
  return (
    <div>
      subject:{subject} roles:{roles.join(",")}
    </div>
  );
}

describe("AuthProvider", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("provides the identity resolved from userinfo", async () => {
    vi.mocked(idp.userinfo).mockResolvedValue({ subject: "alice", roles: ["admin", "operator"] });

    render(
      <AuthProvider>
        <Probe />
      </AuthProvider>,
    );

    await waitFor(() => screen.getByText("subject:alice roles:admin,operator"));
  });

  it("falls back to an empty identity when userinfo fails", async () => {
    vi.mocked(idp.userinfo).mockRejectedValue(new Error("unauthorized"));

    render(
      <AuthProvider>
        <Probe />
      </AuthProvider>,
    );

    await waitFor(() => screen.getByText("subject: roles:"));
  });

  it("does not update state after unmounting while the lookup is still pending", async () => {
    const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    let resolveUserinfo!: (info: { subject: string; roles: string[] }) => void;
    vi.mocked(idp.userinfo).mockReturnValue(
      new Promise((resolve) => {
        resolveUserinfo = resolve;
      }),
    );

    const { unmount } = render(
      <AuthProvider>
        <Probe />
      </AuthProvider>,
    );
    unmount();
    resolveUserinfo({ subject: "alice", roles: ["admin"] });
    await Promise.resolve();
    await Promise.resolve();

    expect(errorSpy).not.toHaveBeenCalled();
  });

  it("does not update state after unmounting while the lookup is still failing", async () => {
    const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    let rejectUserinfo!: (err: Error) => void;
    vi.mocked(idp.userinfo).mockReturnValue(
      new Promise((_resolve, reject) => {
        rejectUserinfo = reject;
      }),
    );

    const { unmount } = render(
      <AuthProvider>
        <Probe />
      </AuthProvider>,
    );
    unmount();
    rejectUserinfo(new Error("unauthorized"));
    await Promise.resolve();
    await Promise.resolve();

    expect(errorSpy).not.toHaveBeenCalled();
  });
});
