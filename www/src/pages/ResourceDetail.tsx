// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { type ReactNode, useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";

import { type GenericResource, deleteResource, getResource } from "../api/generic";
import { Fields, PhaseBadge, STATUS_SKIP } from "../components/Fields";
import { Button } from "../components/ui/button";
import { getPath } from "../lib/path";
import { RESOURCES, type FieldSchema } from "../registry";

// renderFieldValue renders one spec value for display, using the field's
// type so a reference links to the resource it points at instead of
// printing a bare uid, and group/keyValue/stringList get a readable form
// instead of raw JSON.
function renderFieldValue(field: FieldSchema, value: unknown): ReactNode {
  if (value === undefined || value === null || value === "") {
    return <span className="muted">—</span>;
  }
  switch (field.type) {
    case "reference":
      return (
        <Link to={`/${field.referenceKind}/${String(value)}`} className="text-indigo-600 hover:underline">
          {String(value)}
        </Link>
      );
    case "boolean":
      return value ? "true" : "false";
    case "stringList": {
      const items = value as string[];
      return items.length > 0 ? items.join(", ") : <span className="muted">—</span>;
    }
    case "keyValue": {
      const entries = Object.entries(value as Record<string, string>);
      return entries.length > 0 ? entries.map(([k, v]) => `${k}=${v}`).join(", ") : <span className="muted">—</span>;
    }
    case "group": {
      const rows = value as Record<string, unknown>[];
      if (rows.length === 0) return <span className="muted">—</span>;
      return (
        <ul className="list-disc space-y-0.5 pl-4">
          {rows.map((row, i) => (
            <li key={i}>
              {Object.entries(row)
                .map(([k, v]) => `${k}: ${String(v)}`)
                .join(", ")}
            </li>
          ))}
        </ul>
      );
    }
    default:
      return String(value);
  }
}

function FieldRow({ field, value }: { field: FieldSchema; value: unknown }) {
  return (
    <>
      <dt className="font-medium text-slate-500">{field.label}</dt>
      <dd className="text-slate-900">{renderFieldValue(field, value)}</dd>
    </>
  );
}

// ResourceDetail is the generic detail page for every resource kind: full
// metadata, a registry-driven spec view (reference fields link to the
// resource they point at), raw status, and edit/delete actions.
export default function ResourceDetail() {
  const { kind = "", uid = "" } = useParams();
  const navigate = useNavigate();
  const [resource, setResource] = useState<GenericResource | null>(null);
  const [error, setError] = useState("");
  const def = RESOURCES[kind];

  useEffect(() => {
    if (!def) return;
    setError("");
    setResource(null);
    getResource(kind, uid)
      .then(setResource)
      .catch(() => setError("Could not load this resource."));
  }, [def, kind, uid]);

  if (!def) {
    return <p className="text-sm text-red-600">Unknown resource kind &quot;{kind}&quot;.</p>;
  }

  async function onDelete() {
    if (!window.confirm(`Delete ${kind} "${uid}"? This cannot be undone.`)) {
      return;
    }
    try {
      await deleteResource(kind, uid);
      navigate(`/${kind}`);
    } catch {
      setError("Could not delete this resource.");
    }
  }

  return (
    <section className="max-w-3xl space-y-4">
      <Link to={`/${kind}`} className="text-sm text-indigo-600 hover:underline">
        ← Back to {def.pluralLabel}
      </Link>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {resource && (
        <>
          <div className="flex items-center justify-between">
            <h2 className="text-lg font-semibold text-slate-900">{resource.metadata.uid}</h2>
            <PhaseBadge phase={resource.status.phase} />
          </div>

          <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 rounded-lg border border-slate-200 bg-white p-4 text-sm">
            <dt className="font-medium text-slate-500">Kind</dt>
            <dd className="text-slate-900">{def.label}</dd>
            <dt className="font-medium text-slate-500">Generation</dt>
            <dd className="text-slate-900">{resource.metadata.generation}</dd>
            <dt className="font-medium text-slate-500">Created</dt>
            <dd className="text-slate-900">{new Date(resource.metadata.createdAt).toLocaleString()}</dd>
          </dl>

          <div className="space-y-2 rounded-lg border border-slate-200 bg-white p-4">
            <h3 className="text-sm font-semibold text-slate-700">Spec</h3>
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
              {def.fields.map((field) => (
                <FieldRow key={field.key} field={field} value={getPath(resource.spec, field.key)} />
              ))}
            </dl>
          </div>

          <div className="space-y-2 rounded-lg border border-slate-200 bg-white p-4">
            <h3 className="text-sm font-semibold text-slate-700">Status</h3>
            <Fields data={resource.status} skip={STATUS_SKIP} />
          </div>

          <div className="flex gap-2">
            <Button variant="outline" onClick={() => navigate(`/${kind}/${uid}/edit`)}>
              Edit
            </Button>
            <Button variant="outline" onClick={onDelete}>
              Delete
            </Button>
          </div>
        </>
      )}
    </section>
  );
}
