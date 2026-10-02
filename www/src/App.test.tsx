// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import App from "./App";

afterEach(cleanup);

test("renders cloud identity and description", () => {
  render(<App />);
  expect(screen.getByRole("heading", { name: "cloud" }).tagName).toBe("H1");
  expect(screen.getByText("A declarative, Linux-native cloud control plane.")).toBeDefined();
  expect(screen.getByRole("img", { name: "cloud" }).getAttribute("src")).toBeTruthy();
});
