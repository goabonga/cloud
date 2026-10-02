// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { formatValue } from "../lib/path";

// Status keys that are framework noise rather than resource-specific state.
export const STATUS_SKIP = new Set(["phase", "conditions", "observedGeneration"]);

// PhaseBadge renders a lifecycle phase as a colour-coded pill.
export function PhaseBadge({ phase }: { phase?: string }) {
  const p = phase ?? "";
  let cls = "badge";
  if (p === "Ready") cls += " ready";
  else if (p === "Error") cls += " error";
  else if (p === "Pending" || p === "Reconciling" || p === "Deleting") cls += " pending";
  return <span className={cls}>{p || "-"}</span>;
}

// Fields renders an object's entries as compact key/value chips, dropping empty
// values and any keys in skip.
export function Fields({ data, skip }: { data: Record<string, unknown>; skip?: Set<string> }) {
  const entries = Object.entries(data).filter(([k, v]) => {
    if (skip?.has(k)) return false;
    const s = formatValue(v);
    return s !== "" && s !== "{}" && s !== "[]" && s !== "null";
  });
  if (entries.length === 0) return <span className="muted">-</span>;
  return (
    <div className="fields">
      {entries.map(([k, v]) => (
        <span className="field" key={k}>
          <span className="k">{k}</span>
          <span className="v">{formatValue(v)}</span>
        </span>
      ))}
    </div>
  );
}
