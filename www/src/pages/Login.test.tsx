// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as idp from "../api/idp";
import { getToken, setToken } from "../auth";
import Login from "./Login";

vi.mock("../api/idp");

describe("Login", () => {
  beforeEach(() => {
    setToken("");
    vi.restoreAllMocks();
  });

  it("stores the token and navigates home on success", async () => {
    vi.mocked(idp.login).mockResolvedValue("a-token");

    render(
      <MemoryRouter initialEntries={["/login"]}>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="/" element={<div>home</div>} />
        </Routes>
      </MemoryRouter>,
    );

    fireEvent.change(screen.getByLabelText("Username"), { target: { value: "alice" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "s3cr3t" } });
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));

    await waitFor(() => screen.getByText("home"));
    expect(getToken()).toBe("a-token");
    expect(idp.login).toHaveBeenCalledWith("alice", "s3cr3t");
  });

  it("shows an error and does not navigate on failure", async () => {
    vi.mocked(idp.login).mockRejectedValue(new Error("nope"));

    render(
      <MemoryRouter initialEntries={["/login"]}>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="/" element={<div>home</div>} />
        </Routes>
      </MemoryRouter>,
    );

    fireEvent.change(screen.getByLabelText("Username"), { target: { value: "alice" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "wrong" } });
    fireEvent.click(screen.getByRole("button", { name: /sign in/i }));

    await waitFor(() => screen.getByText(/invalid username or password/i));
    expect(getToken()).toBe("");
  });
});
