// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Fields, PhaseBadge, STATUS_SKIP } from "./Fields";

describe("PhaseBadge", () => {
  it("renders a dash for an unset phase", () => {
    render(<PhaseBadge />);
    screen.getByText("-");
  });

  it("marks Ready as the ready style", () => {
    render(<PhaseBadge phase="Ready" />);
    expect(screen.getByText("Ready").className).toContain("ready");
  });

  it("marks Error as the error style", () => {
    render(<PhaseBadge phase="Error" />);
    expect(screen.getByText("Error").className).toContain("error");
  });

  it.each(["Pending", "Reconciling", "Deleting"])("marks %s as the pending style", (phase) => {
    render(<PhaseBadge phase={phase} />);
    expect(screen.getByText(phase).className).toContain("pending");
  });

  it("renders an unrecognised phase with the base style only", () => {
    render(<PhaseBadge phase="Weird" />);
    const el = screen.getByText("Weird");
    expect(el.className).toBe("badge");
  });
});

describe("Fields", () => {
  it("renders a dash when every entry is dropped", () => {
    render(<Fields data={{ phase: "Ready", empty: "", obj: {}, arr: [], n: null }} skip={STATUS_SKIP} />);
    screen.getByText("-");
  });

  it("renders the surviving key/value chips, dropping skipped and empty ones", () => {
    render(<Fields data={{ phase: "Ready", ip: "10.0.0.5", ready: true }} skip={STATUS_SKIP} />);
    screen.getByText("ip");
    screen.getByText("10.0.0.5");
    screen.getByText("ready");
    screen.getByText("true");
    expect(screen.queryByText("phase")).toBeNull();
  });

  it("renders every entry when no skip set is given", () => {
    render(<Fields data={{ note: "hi" }} />);
    screen.getByText("note");
    screen.getByText("hi");
  });
});
