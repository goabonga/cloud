// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as idp from "../api/idp";
import Device from "./Device";

vi.mock("../api/idp");

describe("Device", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("pre-fills the code from the query string and approves it", async () => {
    vi.mocked(idp.deviceVerify).mockResolvedValue(undefined);

    render(
      <MemoryRouter initialEntries={["/device?user_code=WDJB-MJHT"]}>
        <Routes>
          <Route path="/device" element={<Device />} />
        </Routes>
      </MemoryRouter>,
    );

    expect((screen.getByLabelText("Code") as HTMLInputElement).value).toBe("WDJB-MJHT");
    fireEvent.click(screen.getByRole("button", { name: /approve/i }));

    await waitFor(() => screen.getByText("Device approved"));
    expect(idp.deviceVerify).toHaveBeenCalledWith("WDJB-MJHT", true);
  });

  it("denies the code", async () => {
    vi.mocked(idp.deviceVerify).mockResolvedValue(undefined);

    render(
      <MemoryRouter initialEntries={["/device?user_code=WDJB-MJHT"]}>
        <Routes>
          <Route path="/device" element={<Device />} />
        </Routes>
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole("button", { name: /deny/i }));

    await waitFor(() => screen.getByText("Device login denied"));
    expect(idp.deviceVerify).toHaveBeenCalledWith("WDJB-MJHT", false);
  });

  it("shows an error for an invalid code", async () => {
    vi.mocked(idp.deviceVerify).mockRejectedValue(new Error("not found"));

    render(
      <MemoryRouter initialEntries={["/device"]}>
        <Routes>
          <Route path="/device" element={<Device />} />
        </Routes>
      </MemoryRouter>,
    );

    fireEvent.change(screen.getByLabelText("Code"), { target: { value: "NOPE-NOPE" } });
    fireEvent.click(screen.getByRole("button", { name: /approve/i }));

    await waitFor(() => screen.getByText(/invalid or has expired/i));
  });
});
