// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import Sidebar from "./Sidebar";

describe("Sidebar", () => {
  it("renders every category collapsed by default", () => {
    render(
      <MemoryRouter initialEntries={["/"]}>
        <Sidebar />
      </MemoryRouter>,
    );

    screen.getByText("VPC network");
    screen.getByText("Security");
    screen.getByText("Compute");
    expect(screen.queryByText("VPCs")).toBeNull();
  });

  it("expands a category's resources on click, and collapses them again", () => {
    render(
      <MemoryRouter initialEntries={["/"]}>
        <Sidebar />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByText("VPC network"));
    screen.getByText("VPCs");
    screen.getByText("Subnets");

    fireEvent.click(screen.getByText("VPC network"));
    expect(screen.queryByText("VPCs")).toBeNull();
  });

  it("starts the active route's category expanded", () => {
    render(
      <MemoryRouter initialEntries={["/compute"]}>
        <Sidebar />
      </MemoryRouter>,
    );

    screen.getByText("Compute instances");
    expect(screen.queryByText("VPCs")).toBeNull();
  });
});
