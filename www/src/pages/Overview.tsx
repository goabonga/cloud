// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { useEffect, useState } from "react";

import { listResources } from "../api/generic";

const TILES = ["vpc", "subnet", "security_group", "disk", "compute", "load_balancer", "node", "node_pool"] as const;

export default function Overview() {
  const [counts, setCounts] = useState<Record<string, number>>({});
  const [error, setError] = useState("");

  useEffect(() => {
    Promise.all(TILES.map((k) => listResources(k).then((items) => [k, items.length] as const)))
      .then((pairs) => setCounts(Object.fromEntries(pairs)))
      .catch((e: unknown) => setError(String(e)));
  }, []);

  return (
    <section className="space-y-4">
      <h2 className="text-lg font-semibold text-slate-900">Overview</h2>
      {error && <p className="text-sm text-red-600">{error}</p>}
      <div className="flex flex-wrap gap-4">
        {TILES.map((k) => (
          <div key={k} className="min-w-36 rounded-lg border border-slate-200 bg-white px-5 py-4">
            <div className="text-sm text-slate-500">{k}</div>
            <div className="text-3xl font-bold text-slate-900">{counts[k] ?? "-"}</div>
          </div>
        ))}
      </div>
    </section>
  );
}
