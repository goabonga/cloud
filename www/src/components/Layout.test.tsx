// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as idp from "../api/idp";
import { setToken } from "../auth";
import Layout from "./Layout";

vi.mock("../api/idp");

describe("Layout", () => {
  beforeEach(() => {
    setToken("");
    vi.restoreAllMocks();
  });

  it("redirects to /login when there is no token", () => {
    render(
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route path="/login" element={<div>login page</div>} />
          <Route element={<Layout />}>
            <Route index element={<div>overview</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );

    screen.getByText("login page");
  });

  it("renders the shell when a token is present", () => {
    setToken("a-token");
    vi.mocked(idp.userinfo).mockResolvedValue({ subject: "alice", roles: ["admin"] });

    render(
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route path="/login" element={<div>login page</div>} />
          <Route element={<Layout />}>
            <Route index element={<div>overview</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );

    screen.getByText("overview");
  });

  it("shows the IAM nav links only for an admin", async () => {
    setToken("a-token");
    vi.mocked(idp.userinfo).mockResolvedValue({ subject: "alice", roles: ["admin"] });

    render(
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route element={<Layout />}>
            <Route index element={<div>overview</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );

    await waitFor(() => screen.getByText("Users"));
    screen.getByText("Access tokens");
  });

  it("hides the IAM nav links for a non-admin", async () => {
    setToken("a-token");
    vi.mocked(idp.userinfo).mockResolvedValue({ subject: "bob", roles: [] });

    render(
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route element={<Layout />}>
            <Route index element={<div>overview</div>} />
          </Route>
        </Routes>
      </MemoryRouter>,
    );

    await waitFor(() => screen.getByText("overview"));
    expect(screen.queryByText("Users")).toBeNull();
  });
});
