// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { useParams } from "react-router-dom";

import ResourceTable from "../components/ResourceTable";
import { RESOURCES } from "../registry";

// ResourceList is the generic list page for every resource kind: it looks up
// the kind's ResourceDef from the route and hands it to ResourceTable.
export default function ResourceList() {
  const { kind = "" } = useParams();
  const def = RESOURCES[kind];

  if (!def) {
    return <p className="text-sm text-red-600">Unknown resource kind &quot;{kind}&quot;.</p>;
  }

  return (
    <section className="space-y-4">
      <h2 className="text-lg font-semibold text-slate-900">{def.pluralLabel}</h2>
      <ResourceTable def={def} key={def.kind} />
    </section>
  );
}
