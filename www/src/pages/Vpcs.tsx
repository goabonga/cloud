// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { type FormEvent, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";

import { type VPC, createVPC, deleteVPC, listVPCs } from "../api/client";
import { PhaseBadge } from "../components/Fields";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { SortableHeader, type SortState, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "../components/ui/table";

export default function Vpcs() {
  const [items, setItems] = useState<VPC[]>([]);
  const [uid, setUid] = useState("");
  const [cidr, setCidr] = useState("10.0.0.0/16");
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState<SortState | null>(null);

  function reload() {
    listVPCs()
      .then(setItems)
      .catch((e: unknown) => setError(String(e)));
  }

  useEffect(reload, []);

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    let rows = items;
    if (q) {
      rows = rows.filter((v) => v.metadata.uid.toLowerCase().includes(q) || v.spec.cidr.toLowerCase().includes(q));
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
      await createVPC(uid, { cidr });
      setUid("");
      reload();
    } catch (e) {
      setError(String(e));
    }
  }

  async function onDelete(id: string) {
    setError("");
    try {
      await deleteVPC(id);
      reload();
    } catch (e) {
      setError(String(e));
    }
  }

  return (
    <section className="space-y-4">
      <h2 className="text-lg font-semibold text-slate-900">VPCs</h2>
      {error && <p className="text-sm text-red-600">{error}</p>}

      <form onSubmit={onCreate} className="flex flex-wrap items-end gap-3 rounded-lg border border-slate-200 bg-white p-4">
        <div className="space-y-1">
          <Label htmlFor="vpc-uid">UID</Label>
          <Input id="vpc-uid" value={uid} onChange={(e) => setUid(e.target.value)} placeholder="vpc-1" required />
        </div>
        <div className="space-y-1">
          <Label htmlFor="vpc-cidr">CIDR</Label>
          <Input id="vpc-cidr" value={cidr} onChange={(e) => setCidr(e.target.value)} required />
        </div>
        <Button type="submit">Create</Button>
      </form>

      <Input
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        placeholder="Search by UID or CIDR…"
        className="max-w-xs"
      />

      <Table>
        <TableHeader>
          <TableRow>
            <SortableHeader label="UID" sortKey="uid" active={sort} onSort={onSort} />
            <TableHead>CIDR</TableHead>
            <SortableHeader label="Phase" sortKey="phase" active={sort} onSort={onSort} />
            <TableHead>Bridge</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {visible.map((v) => (
            <TableRow key={v.metadata.uid}>
              <TableCell>
                <Link to={`/resources/vpc/${v.metadata.uid}`} className="font-medium text-indigo-600 hover:underline">
                  {v.metadata.uid}
                </Link>
              </TableCell>
              <TableCell>{v.spec.cidr}</TableCell>
              <TableCell>
                <PhaseBadge phase={v.status.phase} />
              </TableCell>
              <TableCell>{v.status.bridgeName ?? "-"}</TableCell>
              <TableCell className="text-right">
                <Button size="sm" variant="outline" onClick={() => onDelete(v.metadata.uid)}>
                  Delete
                </Button>
              </TableCell>
            </TableRow>
          ))}
          {visible.length === 0 && (
            <TableRow>
              <TableCell colSpan={5} className="text-slate-500">
                No VPCs.
              </TableCell>
            </TableRow>
          )}
        </TableBody>
      </Table>
    </section>
  );
}
