// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import {
  type ColumnDef,
  type PaginationState,
  type Row,
  type SortingState,
  flexRender,
  getCoreRowModel,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useReactTable,
} from "@tanstack/react-table";
import { ChevronDown, ChevronUp, ChevronsUpDown } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";

import { type GenericResource, deleteResource, listResources } from "../api/generic";
import { formatValue, getPath } from "../lib/path";
import type { ResourceDef } from "../registry";
import { PhaseBadge } from "./Fields";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Label } from "./ui/label";

const PAGE_SIZES = [10, 25, 50, 100];

// ResourceTable lists one resource kind: a searchable, sortable, paginated
// table driven entirely by the kind's ResourceDef, plus create/edit/delete
// actions (create and edit navigate to the schema-driven ResourceForm page).
export default function ResourceTable({ def }: { def: ResourceDef }) {
  const navigate = useNavigate();
  const [items, setItems] = useState<GenericResource[]>([]);
  const [error, setError] = useState("");
  const [globalFilter, setGlobalFilter] = useState("");
  const [sorting, setSorting] = useState<SortingState>([]);
  const [pagination, setPagination] = useState<PaginationState>({ pageIndex: 0, pageSize: PAGE_SIZES[0] });

  function reload() {
    listResources(def.kind)
      .then(setItems)
      .catch((e: unknown) => setError(String(e)));
  }

  useEffect(reload, [def.kind]);

  const searchPaths = useMemo(() => ["metadata.uid", ...def.searchableFields], [def.searchableFields]);

  const columns = useMemo<ColumnDef<GenericResource>[]>(() => {
    const base: ColumnDef<GenericResource>[] = [
      {
        id: "uid",
        header: "UID",
        accessorFn: (r) => r.metadata.uid,
        cell: (info) => <span className="font-medium text-slate-900">{info.getValue<string>()}</span>,
      },
      {
        id: "phase",
        header: "Phase",
        accessorFn: (r) => r.status.phase ?? "",
        cell: (info) => <PhaseBadge phase={info.getValue<string>()} />,
      },
      ...def.listColumns.map<ColumnDef<GenericResource>>((col) => ({
        id: col.key,
        header: col.label,
        accessorFn: (r) => getPath(r, col.key),
        cell: (info) => formatValue(info.getValue()),
      })),
    ];
    return base;
  }, [def.listColumns]);

  const table = useReactTable({
    data: items,
    columns,
    state: { sorting, globalFilter, pagination },
    onSortingChange: setSorting,
    onGlobalFilterChange: setGlobalFilter,
    onPaginationChange: setPagination,
    globalFilterFn: (row: Row<GenericResource>, _columnId, filterValue: string) => {
      const q = filterValue.trim().toLowerCase();
      if (!q) return true;
      return searchPaths.some((p) => formatValue(getPath(row.original, p)).toLowerCase().includes(q));
    },
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
  });

  async function onDelete(itemUid: string) {
    if (!window.confirm(`Delete ${def.kind} "${itemUid}"? This cannot be undone.`)) {
      return;
    }
    setError("");
    try {
      await deleteResource(def.kind, itemUid);
      reload();
    } catch (e) {
      setError(String(e));
    }
  }

  const rows = table.getRowModel().rows;
  const pageCount = table.getPageCount();

  return (
    <div className="space-y-4">
      {error && <p className="text-sm text-red-600">{error}</p>}

      <div className="flex items-center justify-between gap-3">
        <Input
          value={globalFilter}
          onChange={(e) => setGlobalFilter(e.target.value)}
          placeholder={`Search ${def.pluralLabel.toLowerCase()}…`}
          className="max-w-xs"
        />
        <Button onClick={() => navigate(`/${def.kind}/new`)}>Create {def.label.toLowerCase()}</Button>
      </div>

      <div className="overflow-x-auto rounded-lg border border-slate-200 bg-white">
        <table className="w-full border-collapse text-sm">
          <thead className="bg-slate-50">
            {table.getHeaderGroups().map((headerGroup) => (
              <tr key={headerGroup.id}>
                {headerGroup.headers.map((header) => {
                  const sort = header.column.getIsSorted();
                  return (
                    <th
                      key={header.id}
                      className="cursor-pointer px-4 py-2 text-left text-xs font-semibold tracking-wide text-slate-500 uppercase select-none"
                      onClick={header.column.getToggleSortingHandler()}
                    >
                      <span className="inline-flex items-center gap-1">
                        {flexRender(header.column.columnDef.header, header.getContext())}
                        {header.column.getCanSort() &&
                          (sort === "asc" ? (
                            <ChevronUp className="h-3 w-3" aria-hidden="true" />
                          ) : sort === "desc" ? (
                            <ChevronDown className="h-3 w-3" aria-hidden="true" />
                          ) : (
                            <ChevronsUpDown className="h-3 w-3 text-slate-300" aria-hidden="true" />
                          ))}
                      </span>
                    </th>
                  );
                })}
                <th className="px-4 py-2" />
              </tr>
            ))}
          </thead>
          <tbody className="divide-y divide-slate-100">
            {rows.map((row) => (
              <tr key={row.id} className="hover:bg-slate-50">
                {row.getVisibleCells().map((cell) => (
                  <td key={cell.id} className="px-4 py-2 text-slate-700">
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </td>
                ))}
                <td className="flex justify-end gap-2 px-4 py-2 text-right">
                  <Button size="sm" variant="outline" onClick={() => navigate(`/${def.kind}/${row.original.metadata.uid}/edit`)}>
                    Edit
                  </Button>
                  <Button size="sm" variant="outline" onClick={() => onDelete(row.original.metadata.uid)}>
                    Delete
                  </Button>
                </td>
              </tr>
            ))}
            {rows.length === 0 && (
              <tr>
                <td colSpan={columns.length + 1} className="px-4 py-6 text-center text-slate-500">
                  No {def.pluralLabel.toLowerCase()}.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="flex items-center justify-between text-sm text-slate-600">
        <div className="flex items-center gap-2">
          <Label htmlFor={`${def.kind}-page-size`}>Rows per page</Label>
          <select
            id={`${def.kind}-page-size`}
            value={pagination.pageSize}
            onChange={(e) => table.setPageSize(Number(e.target.value))}
            className="h-8 rounded-md border border-slate-300 bg-white px-2 text-sm"
          >
            {PAGE_SIZES.map((size) => (
              <option key={size} value={size}>
                {size}
              </option>
            ))}
          </select>
        </div>
        <div className="flex items-center gap-3">
          <span>
            Page {pageCount === 0 ? 0 : pagination.pageIndex + 1} of {pageCount}
          </span>
          <Button size="sm" variant="outline" onClick={() => table.previousPage()} disabled={!table.getCanPreviousPage()}>
            Previous
          </Button>
          <Button size="sm" variant="outline" onClick={() => table.nextPage()} disabled={!table.getCanNextPage()}>
            Next
          </Button>
        </div>
      </div>
    </div>
  );
}
