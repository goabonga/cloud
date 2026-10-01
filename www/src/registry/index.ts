// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { RESOURCES } from "./resources";
import type { CategoryKey, ResourceDef } from "./types";

export { CATEGORIES } from "./categories";
export { RESOURCES } from "./resources";
export type { CategoryDef, CategoryKey, FieldSchema, FieldType, ListColumn, ResourceDef } from "./types";

// resourcesByCategory returns a category's resource kinds, in the order
// they're declared in RESOURCES.
export function resourcesByCategory(key: CategoryKey): ResourceDef[] {
  return Object.values(RESOURCES).filter((r) => r.category === key);
}

// categoryOf looks up the category a kind belongs to, if any.
export function categoryOf(kind: string): CategoryKey | undefined {
  return RESOURCES[kind]?.category;
}
