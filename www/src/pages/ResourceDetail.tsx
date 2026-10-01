// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";

import { type GenericResource, deleteResource, getResource } from "../api/generic";
import { Breadcrumbs } from "../components/Breadcrumbs";
import { Fields, PhaseBadge, STATUS_SKIP } from "../components/Fields";
import { Button } from "../components/ui/button";

// listRouteFor resolves the list page a kind's rows link in from, so the
// breadcrumb trail goes back to where the user actually came from.
function listRouteFor(kind: string): { label: string; to: string } {
  switch (kind) {
    case "vpc":
      return { label: "VPCs", to: "/vpcs" };
    case "acl_policy":
      return { label: "ACL policies", to: "/acls" };
    default:
      return { label: "Resources", to: "/resources" };
  }
}

export default function ResourceDetail() {
  const { kind = "", uid = "" } = useParams();
  const navigate = useNavigate();
  const [resource, setResource] = useState<GenericResource | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    setError("");
    setResource(null);
    getResource(kind, uid)
      .then(setResource)
      .catch(() => setError("Could not load this resource."));
  }, [kind, uid]);

  const list = listRouteFor(kind);

  async function onDelete() {
    if (!window.confirm(`Delete ${kind} "${uid}"? This cannot be undone.`)) {
      return;
    }
    try {
      await deleteResource(kind, uid);
      navigate(list.to);
    } catch {
      setError("Could not delete this resource.");
    }
  }

  return (
    <section className="max-w-3xl space-y-4">
      <Breadcrumbs items={[{ label: list.label, to: list.to }, { label: uid }]} />
      {error && <p className="text-sm text-red-600">{error}</p>}
      {resource && (
        <>
          <div className="flex items-center justify-between">
            <h2 className="text-lg font-semibold text-slate-900">{resource.metadata.uid}</h2>
            <PhaseBadge phase={resource.status.phase} />
          </div>
          <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 rounded-lg border border-slate-200 bg-white p-4 text-sm">
            <dt className="font-medium text-slate-500">Kind</dt>
            <dd className="text-slate-900">{kind}</dd>
            <dt className="font-medium text-slate-500">Generation</dt>
            <dd className="text-slate-900">{resource.metadata.generation}</dd>
            <dt className="font-medium text-slate-500">Created</dt>
            <dd className="text-slate-900">{new Date(resource.metadata.createdAt).toLocaleString()}</dd>
          </dl>
          <div className="space-y-2 rounded-lg border border-slate-200 bg-white p-4">
            <h3 className="text-sm font-semibold text-slate-700">Spec</h3>
            <Fields data={resource.spec} />
          </div>
          <div className="space-y-2 rounded-lg border border-slate-200 bg-white p-4">
            <h3 className="text-sm font-semibold text-slate-700">Status</h3>
            <Fields data={resource.status} skip={STATUS_SKIP} />
          </div>
          <Button variant="outline" onClick={onDelete}>
            Delete
          </Button>
        </>
      )}
    </section>
  );
}
