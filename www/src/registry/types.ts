// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import type { LucideIcon } from "lucide-react";

// CategoryKey groups resource kinds the way the GCP console groups services
// in its left nav (one collapsible section per key).
export type CategoryKey = "networking" | "security" | "compute" | "storage" | "loadbalancing" | "dns";

export interface CategoryDef {
  key: CategoryKey;
  label: string;
  icon: LucideIcon;
}

// ListColumn renders one extra table column besides the always-present UID
// and phase. key is a dot-path resolved against the resource envelope (e.g.
// "spec.cidr", "status.bridgeName", "spec.rules.length").
export interface ListColumn {
  key: string;
  label: string;
}

// FieldType picks which control FieldInput renders and how ResourceForm
// assembles its value into the spec.
// "text" is a multi-line "string", for values like file contents or
// cloud-init user-data.
export type FieldType =
  | "string"
  | "text"
  | "number"
  | "boolean"
  | "enum"
  | "stringList"
  | "keyValue"
  | "reference"
  | "group";

// FieldSchema is one create/edit form field. key is a dot-path assembled
// into the spec on submit (e.g. "capacity.cpus" -> { capacity: { cpus } });
// a "group" field instead holds an array of rows, each assembled from its
// own `fields`.
export interface FieldSchema {
  key: string;
  label: string;
  type: FieldType;
  required?: boolean;
  // enumValues lists the options for "enum".
  enumValues?: string[];
  // referenceKind names the kind a "reference" field's <select> is
  // populated from (via listResources(referenceKind)).
  referenceKind?: string;
  // fields lists a "group" field's per-row sub-fields.
  fields?: FieldSchema[];
  helpText?: string;
}

// ResourceDef is the single source of truth for how one resource kind shows
// up in the console: which category it lives under, its sidebar icon, how
// its list table renders and searches, and its create/edit form fields.
export interface ResourceDef {
  kind: string;
  label: string;
  pluralLabel: string;
  category: CategoryKey;
  icon: LucideIcon;
  listColumns: ListColumn[];
  // searchableFields are dot-paths checked by the list search box, in
  // addition to metadata.uid which is always searchable.
  searchableFields: string[];
  fields: FieldSchema[];
}
