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

// ResourceDef is the single source of truth for how one resource kind shows
// up in the console: which category it lives under, its sidebar icon, and
// how its list table renders and searches.
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
}
