// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { Link } from "react-router-dom";

export interface Crumb {
  label: string;
  to?: string;
}

// Breadcrumbs renders a trail of links; the last item (or any without a
// `to`) is plain text, not a link.
export function Breadcrumbs({ items }: { items: Crumb[] }) {
  return (
    <nav aria-label="Breadcrumb" className="mb-4 text-sm text-slate-500">
      {items.map((item, i) => (
        <span key={`${item.label}-${i}`}>
          {i > 0 && <span className="mx-1.5 text-slate-300">/</span>}
          {item.to ? (
            <Link to={item.to} className="hover:text-slate-900 hover:underline">
              {item.label}
            </Link>
          ) : (
            <span className="text-slate-900">{item.label}</span>
          )}
        </span>
      ))}
    </nav>
  );
}
