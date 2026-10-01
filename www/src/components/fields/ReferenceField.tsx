// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { useEffect, useState } from "react";

import { type GenericResource, listResources } from "../../api/generic";
import type { FieldSchema } from "../../registry/types";

// ReferenceField renders a <select> populated from another resource kind's
// current items, used for foreign-id fields like ComputeSpec.SubnetID.
export default function ReferenceField({
  id,
  field,
  value,
  onChange,
}: {
  id: string;
  field: FieldSchema;
  value: string;
  onChange: (value: string) => void;
}) {
  const [items, setItems] = useState<GenericResource[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!field.referenceKind) return;
    listResources(field.referenceKind)
      .then(setItems)
      .catch(() => setError(`Could not load ${field.referenceKind} options.`));
  }, [field.referenceKind]);

  return (
    <div className="space-y-1">
      <select
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="h-9 w-full rounded-md border border-slate-300 bg-white px-3 text-sm"
      >
        <option value="">—</option>
        {items.map((item) => (
          <option key={item.metadata.uid} value={item.metadata.uid}>
            {item.metadata.name ? `${item.metadata.uid} (${item.metadata.name})` : item.metadata.uid}
          </option>
        ))}
      </select>
      {error && <p className="text-xs text-red-600">{error}</p>}
    </div>
  );
}
