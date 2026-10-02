// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { listResources } from "../api/generic";
import { CATEGORIES, RESOURCES, resourcesByCategory } from "../registry";

// Overview shows every resource kind's count as a tile, grouped into the
// same categories as the sidebar.
export default function Overview() {
  const [counts, setCounts] = useState<Record<string, number>>({});
  const [error, setError] = useState("");

  useEffect(() => {
    Promise.all(Object.keys(RESOURCES).map((kind) => listResources(kind).then((items) => [kind, items.length] as const)))
      .then((pairs) => setCounts(Object.fromEntries(pairs)))
      .catch((e: unknown) => setError(String(e)));
  }, []);

  return (
    <section className="space-y-6">
      <h2 className="text-lg font-semibold text-slate-900">Overview</h2>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {CATEGORIES.map((category) => (
        <div key={category.key} className="space-y-2">
          <h3 className="text-sm font-semibold tracking-wide text-slate-500 uppercase">{category.label}</h3>
          <div className="flex flex-wrap gap-4">
            {resourcesByCategory(category.key).map((def) => {
              const Icon = def.icon;
              return (
                <Link
                  key={def.kind}
                  to={`/${def.kind}`}
                  className="min-w-36 rounded-lg border border-slate-200 bg-white px-5 py-4 transition-colors hover:border-indigo-300 hover:shadow-sm"
                >
                  <div className="flex items-center gap-1.5 text-sm text-slate-500">
                    <Icon className="h-3.5 w-3.5" aria-hidden="true" />
                    {def.pluralLabel}
                  </div>
                  <div className="text-3xl font-bold text-slate-900">{counts[def.kind] ?? "-"}</div>
                </Link>
              );
            })}
          </div>
        </div>
      ))}
    </section>
  );
}
