// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { type FormEvent, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";

import { type ACLPolicy, createACL, deleteACL, listACLs } from "../api/client";
import { PhaseBadge } from "../components/Fields";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { SortableHeader, type SortState, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";

export default function Acls() {
  const [items, setItems] = useState<ACLPolicy[]>([]);
  const [uid, setUid] = useState("");
  const [action, setAction] = useState("allow");
  const [protocol, setProtocol] = useState("tcp");
  const [port, setPort] = useState("443");
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState<SortState | null>(null);

  function reload() {
    listACLs()
      .then(setItems)
      .catch((e: unknown) => setError(String(e)));
  }

  useEffect(reload, []);

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    let rows = items;
    if (q) {
      rows = rows.filter((p) => p.metadata.uid.toLowerCase().includes(q));
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
    try {
      const portNum = Number(port);
      await createACL(uid, {
        rules: [{ action, protocol, port: Number.isFinite(portNum) && portNum > 0 ? portNum : undefined }],
      });
      setUid("");
      reload();
    } catch (e) {
      setError(String(e));
    }
  }

  async function onDelete(id: string) {
    setError("");
    try {
      await deleteACL(id);
      reload();
    } catch (e) {
      setError(String(e));
    }
  }

  return (
    <section className="space-y-4">
      <h2 className="text-lg font-semibold text-slate-900">ACL policies</h2>
      {error && <p className="text-sm text-red-600">{error}</p>}

      <form onSubmit={onCreate} className="flex flex-wrap items-end gap-3 rounded-lg border border-slate-200 bg-white p-4">
        <div className="space-y-1">
          <Label htmlFor="acl-uid">UID</Label>
          <Input id="acl-uid" value={uid} onChange={(e) => setUid(e.target.value)} placeholder="web" required />
        </div>
        <div className="space-y-1">
          <Label htmlFor="acl-action">Action</Label>
          <select
            id="acl-action"
            value={action}
            onChange={(e) => setAction(e.target.value)}
            className="h-9 rounded-md border border-slate-300 bg-white px-3 text-sm"
          >
            <option value="allow">allow</option>
            <option value="deny">deny</option>
          </select>
        </div>
        <div className="space-y-1">
          <Label htmlFor="acl-protocol">Protocol</Label>
          <select
            id="acl-protocol"
            value={protocol}
            onChange={(e) => setProtocol(e.target.value)}
            className="h-9 rounded-md border border-slate-300 bg-white px-3 text-sm"
          >
            <option value="tcp">tcp</option>
            <option value="udp">udp</option>
            <option value="icmp">icmp</option>
            <option value="all">all</option>
          </select>
        </div>
        <div className="space-y-1">
          <Label htmlFor="acl-port">Port</Label>
          <Input id="acl-port" value={port} onChange={(e) => setPort(e.target.value)} />
        </div>
        <Button type="submit">Create</Button>
      </form>

      <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Search by UID…" className="max-w-xs" />

      <Table>
        <TableHeader>
          <TableRow>
            <SortableHeader label="UID" sortKey="uid" active={sort} onSort={onSort} />
            <TableHead>Rules</TableHead>
            <SortableHeader label="Phase" sortKey="phase" active={sort} onSort={onSort} />
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {visible.map((p) => (
            <TableRow key={p.metadata.uid}>
              <TableCell>
                <Link
                  to={`/resources/acl_policy/${p.metadata.uid}`}
                  className="font-medium text-indigo-600 hover:underline"
                >
                  {p.metadata.uid}
                </Link>
              </TableCell>
              <TableCell>{p.spec.rules.length}</TableCell>
              <TableCell>
                <PhaseBadge phase={p.status.phase} />
              </TableCell>
              <TableCell className="text-right">
                <Button size="sm" variant="outline" onClick={() => onDelete(p.metadata.uid)}>
                  Delete
                </Button>
              </TableCell>
            </TableRow>
          ))}
          {visible.length === 0 && (
            <TableRow>
              <TableCell colSpan={4} className="text-slate-500">
                No ACL policies.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </section>
  );
}
