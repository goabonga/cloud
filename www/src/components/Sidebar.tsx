// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { ChevronDown, ChevronRight } from "lucide-react";
import { useState } from "react";
import { NavLink, useLocation } from "react-router-dom";

import { CATEGORIES, type CategoryKey, categoryOf, resourcesByCategory } from "../registry";

function itemClass({ isActive }: { isActive: boolean }): string {
  return [
    "flex items-center gap-2 rounded-md px-3 py-1.5 text-sm transition-colors",
    isActive ? "bg-slate-800 text-white" : "text-slate-300 hover:bg-slate-800 hover:text-white",
  ].join(" ");
}

// Sidebar renders the resource categories as collapsible sections (GCP
// console style), each listing its kinds. The category containing the
// current route starts expanded; others start collapsed.
export default function Sidebar() {
  const location = useLocation();
  const activeKind = location.pathname.split("/")[1] ?? "";
  const activeCategory = categoryOf(activeKind);
  const [expanded, setExpanded] = useState<Set<CategoryKey>>(() => new Set(activeCategory ? [activeCategory] : []));

  function toggle(key: CategoryKey) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  }

  return (
    <div className="space-y-1">
      {CATEGORIES.map((category) => {
        const items = resourcesByCategory(category.key);
        const isOpen = expanded.has(category.key);
        const CategoryIcon = category.icon;
        return (
          <div key={category.key}>
            <button
              type="button"
              onClick={() => toggle(category.key)}
              aria-expanded={isOpen}
              className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-sm font-medium text-slate-300 transition-colors hover:bg-slate-800 hover:text-white"
            >
              <CategoryIcon className="h-4 w-4 shrink-0" aria-hidden="true" />
              <span className="flex-1 text-left">{category.label}</span>
              {isOpen ? (
                <ChevronDown className="h-4 w-4 shrink-0" aria-hidden="true" />
              ) : (
                <ChevronRight className="h-4 w-4 shrink-0" aria-hidden="true" />
              )}
            </button>
            {isOpen && (
              <div className="mt-0.5 ml-5 space-y-0.5 border-l border-slate-800 pl-2">
                {items.map((item) => {
                  const ItemIcon = item.icon;
                  return (
                    <NavLink key={item.kind} to={`/${item.kind}`} className={itemClass}>
                      <ItemIcon className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                      {item.pluralLabel}
                    </NavLink>
                  );
                })}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
