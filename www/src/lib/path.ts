// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

// getPath resolves a dot-separated path (e.g. "spec.capacity.cpus") against
// a value, returning undefined if any segment is missing. Array `.length` is
// a plain property access, so "spec.records.length" works the same way.
export function getPath(value: unknown, path: string): unknown {
  let current: unknown = value;
  for (const segment of path.split(".")) {
    if (current === null || current === undefined || typeof current !== "object") {
      return undefined;
    }
    current = (current as Record<string, unknown>)[segment];
  }
  return current;
}

// setPath writes value at a dot-separated path into target, creating
// intermediate objects as needed, and returns target. Used to assemble a
// form's flat { "capacity.cpus": 4 } field values back into the nested spec
// shape the API expects.
export function setPath(target: Record<string, unknown>, path: string, value: unknown): Record<string, unknown> {
  const segments = path.split(".");
  let cursor: Record<string, unknown> = target;
  for (let i = 0; i < segments.length - 1; i++) {
    const segment = segments[i];
    const next = cursor[segment];
    if (typeof next !== "object" || next === null) {
      cursor[segment] = {};
    }
    cursor = cursor[segment] as Record<string, unknown>;
  }
  cursor[segments[segments.length - 1]] = value;
  return target;
}

// formatValue renders an arbitrary field value as display text.
export function formatValue(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}
