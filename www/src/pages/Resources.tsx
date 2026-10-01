// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { type FormEvent, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";

import { type GenericResource, KINDS, createResource, deleteResource, listResources } from "../api/generic";
import { Fields, PhaseBadge, STATUS_SKIP } from "../components/Fields";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { SortableHeader, type SortState, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";

export default function Resources() {
  const [kind, setKind] = useState<string>("vpc");
  const [items, setItems] = useState<GenericResource[]>([]);
  const [uid, setUid] = useState("");
  const [specText, setSpecText] = useState("{}");
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState<SortState | null>(null);

  function reload(k: string) {
    listResources(k)
      .then(setItems)
      .catch((e: unknown) => setError(String(e)));
  }

  useEffect(() => {
    setError("");
    setQuery("");
    setSort(null);
    reload(kind);
  }, [kind]);

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    let rows = items;
    if (q) {
      rows = rows.filter((r) => r.metadata.uid.toLowerCase().includes(q));
    }
    if (sort) {
      rows = [...rows].sort((a, b) => {
        const av = sort.key === "phase" ? (a.status.phase ?? "") : a.metadata.uid;
        const bv = sort.key === "phase" ? (b.status.phase ?? "") : b.metadata.uid;
        return sort.dir === "asc" ? av.localeCompare(bv) : bv.localeCompare(av);
      });
    }
    return rows;
  }, [items, query, sort]);

  function onSort(key: string) {
    setSort((prev) => (prev?.key === key ? { key, dir: prev.dir === "asc" ? "desc" : "asc" } : { key, dir: "asc" }));
  }

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    setError("");
    let spec: Record<string, unknown>;
    try {
      spec = JSON.parse(specText) as Record<string, unknown>;
    } catch {
      setError("spec is not valid JSON");
      return;
    }
    try {
      await createResource(kind, uid, spec);
      setUid("");
      reload(kind);
    } catch (e) {
      setError(String(e));
    }
  }

  async function onDelete(id: string) {
    setError("");
    try {
      await deleteResource(kind, id);
      reload(kind);
    } catch (e) {
      setError(String(e));
    }
  }

  return (
    <section className="space-y-4">
      <h2 className="text-lg font-semibold text-slate-900">Resources</h2>
      <p className="text-sm text-slate-600">
        Browse and manage any resource kind. Enter the spec as JSON, for example{" "}
        <code className="rounded bg-slate-100 px-1 py-0.5 text-xs">{`{"vpcId":"...","cidr":"10.0.1.0/24","type":"public"}`}</code>{" "}
        for a subnet.
      </p>

      <div className="space-y-1">
        <Label htmlFor="kind-select">Kind</Label>
        <select
          id="kind-select"
          value={kind}
          onChange={(e) => setKind(e.target.value)}
          className="h-9 rounded-md border border-slate-300 bg-white px-3 text-sm"
        >
          {KINDS.map((k) => (
            <option key={k} value={k}>
              {k}
            </option>
          ))}
        </select>
      </div>

      {error && <p className="text-sm text-red-600">{error}</p>}

      <form onSubmit={onCreate} className="flex flex-wrap items-end gap-3 rounded-lg border border-slate-200 bg-white p-4">
        <div className="space-y-1">
          <Label htmlFor="resource-uid">UID</Label>
          <Input id="resource-uid" value={uid} onChange={(e) => setUid(e.target.value)} placeholder={`${kind}-1`} required />
        </div>
        <div className="min-w-80 flex-1 space-y-1">
          <Label htmlFor="resource-spec">Spec (JSON)</Label>
          <textarea
            id="resource-spec"
            className="w-full rounded-md border border-slate-300 bg-white px-3 py-1.5 font-mono text-xs shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500"
            rows={3}
            value={specText}
            onChange={(e) => setSpecText(e.target.value)}
          />
        </div>
        <Button type="submit">Create</Button>
      </form>

      <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Search by UID…" className="max-w-xs" />

      <Table>
        <TableHeader>
          <TableRow>
            <SortableHeader label="UID" sortKey="uid" active={sort} onSort={onSort} />
            <SortableHeader label="Phase" sortKey="phase" active={sort} onSort={onSort} />
            <TableHead>Spec</TableHead>
            <TableHead>Status</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {visible.map((r) => (
            <TableRow key={r.metadata.uid}>
              <TableCell>
                <Link
                  to={`/resources/${kind}/${r.metadata.uid}`}
                  className="font-medium text-indigo-600 hover:underline"
                >
                  {r.metadata.uid}
                </Link>
              </TableCell>
              <TableCell>
                <PhaseBadge phase={r.status.phase} />
              </TableCell>
              <TableCell>
                <Fields data={r.spec} />
              </TableCell>
              <TableCell>
                <Fields data={r.status} skip={STATUS_SKIP} />
              </TableCell>
              <TableCell className="text-right">
                <Button size="sm" variant="outline" onClick={() => onDelete(r.metadata.uid)}>
                  Delete
                </Button>
              </TableCell>
            </TableRow>
          ))}
          {visible.length === 0 && (
            <TableRow>
              <TableCell colSpan={5} className="text-slate-500">
                No {kind} resources.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </section>
  );
}
