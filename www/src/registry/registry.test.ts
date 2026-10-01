// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { describe, expect, it } from "vitest";

import { KINDS } from "../api/generic";
import { CATEGORIES, RESOURCES } from "./index";

describe("resource registry", () => {
  it("has exactly one entry per kind in KINDS, and no extras", () => {
    const registryKinds = Object.keys(RESOURCES).sort();
    const knownKinds = [...KINDS].sort();
    expect(registryKinds).toEqual(knownKinds);
  });

  it("keys each entry by its own kind", () => {
    for (const [key, def] of Object.entries(RESOURCES)) {
      expect(def.kind).toBe(key);
    }
  });

  it("assigns every resource to a declared category", () => {
    const categoryKeys = new Set(CATEGORIES.map((c) => c.key));
    for (const def of Object.values(RESOURCES)) {
      expect(categoryKeys.has(def.category)).toBe(true);
    }
  });

  it("gives every resource a label, plural label and at least one list column", () => {
    for (const def of Object.values(RESOURCES)) {
      expect(def.label).not.toBe("");
      expect(def.pluralLabel).not.toBe("");
      expect(def.listColumns.length).toBeGreaterThan(0);
    }
  });

  it("has no duplicate category keys", () => {
    const keys = CATEGORIES.map((c) => c.key);
    expect(new Set(keys).size).toBe(keys.length);
  });
});
