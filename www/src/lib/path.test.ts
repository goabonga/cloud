// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { describe, expect, it } from "vitest";

import { formatValue, getPath } from "./path";

describe("getPath", () => {
  it("resolves a nested path", () => {
    expect(getPath({ spec: { capacity: { cpus: 4 } } }, "spec.capacity.cpus")).toBe(4);
  });

  it("resolves an array length through a trailing segment", () => {
    expect(getPath({ spec: { records: ["a", "b"] } }, "spec.records.length")).toBe(2);
  });

  it("returns undefined for a missing path", () => {
    expect(getPath({ spec: {} }, "spec.cidr")).toBeUndefined();
    expect(getPath({ spec: null }, "spec.cidr")).toBeUndefined();
  });
});

describe("formatValue", () => {
  it("renders primitives and booleans", () => {
    expect(formatValue("x")).toBe("x");
    expect(formatValue(42)).toBe("42");
    expect(formatValue(true)).toBe("true");
    expect(formatValue(false)).toBe("false");
  });

  it("renders null/undefined as an empty string", () => {
    expect(formatValue(null)).toBe("");
    expect(formatValue(undefined)).toBe("");
  });

  it("renders objects as JSON", () => {
    expect(formatValue({ a: 1 })).toBe('{"a":1}');
  });
});
