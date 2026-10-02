// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { describe, expect, it } from "vitest";

import { KINDS } from "../api/generic";
import { CATEGORIES, RESOURCES } from "./index";
import type { FieldSchema } from "./types";

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

  it("gives every resource at least one form field, each with a key and label", () => {
    for (const def of Object.values(RESOURCES)) {
      expect(def.fields.length).toBeGreaterThan(0);
      for (const field of def.fields) {
        expect(field.key).not.toBe("");
        expect(field.label).not.toBe("");
      }
    }
  });

  it("requires enumValues on every enum field, and fields on every group field", () => {
    function checkFields(fields: FieldSchema[]) {
      for (const field of fields) {
        if (field.type === "enum") {
          expect(field.enumValues?.length ?? 0).toBeGreaterThan(0);
        }
        if (field.type === "group") {
          expect(field.fields?.length ?? 0).toBeGreaterThan(0);
          checkFields(field.fields ?? []);
        }
        if (field.type === "reference") {
          expect(field.referenceKind).toBeTruthy();
          expect(RESOURCES[field.referenceKind ?? ""]).toBeDefined();
        }
      }
    }
    for (const def of Object.values(RESOURCES)) {
      checkFields(def.fields);
    }
  });
});
