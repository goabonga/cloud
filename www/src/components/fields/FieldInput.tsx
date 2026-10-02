// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { Minus, Plus } from "lucide-react";

import type { FieldSchema } from "../../registry/types";
import { Input } from "../ui/input";
import GroupField from "./GroupField";
import ReferenceField from "./ReferenceField";

export interface FieldControlProps {
  id: string;
  field: FieldSchema;
  value: unknown;
  onChange: (value: unknown) => void;
}

function TextInput({ id, value, onChange }: { id: string; value: unknown; onChange: (v: unknown) => void }) {
  return <Input id={id} value={(value as string) ?? ""} onChange={(e) => onChange(e.target.value)} />;
}

function TextAreaInput({ id, value, onChange }: { id: string; value: unknown; onChange: (v: unknown) => void }) {
  return (
    <textarea
      id={id}
      rows={6}
      value={(value as string) ?? ""}
      onChange={(e) => onChange(e.target.value)}
      className="w-full rounded-md border border-slate-300 bg-white px-3 py-1.5 font-mono text-xs shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500"
    />
  );
}

function NumberInput({ id, value, onChange }: { id: string; value: unknown; onChange: (v: unknown) => void }) {
  return (
    <Input
      id={id}
      type="number"
      value={value === undefined || value === "" ? "" : String(value)}
      onChange={(e) => onChange(e.target.value === "" ? undefined : Number(e.target.value))}
    />
  );
}

function BooleanInput({ id, value, onChange }: { id: string; value: unknown; onChange: (v: unknown) => void }) {
  return (
    <input
      id={id}
      type="checkbox"
      checked={Boolean(value)}
      onChange={(e) => onChange(e.target.checked)}
      className="h-4 w-4 rounded border-slate-300"
    />
  );
}

function EnumInput({
  id,
  field,
  value,
  onChange,
}: {
  id: string;
  field: FieldSchema;
  value: unknown;
  onChange: (v: unknown) => void;
}) {
  return (
    <select
      id={id}
      value={(value as string) ?? ""}
      onChange={(e) => onChange(e.target.value)}
      className="h-9 rounded-md border border-slate-300 bg-white px-3 text-sm"
    >
      <option value="">—</option>
      {field.enumValues?.map((v) => (
        <option key={v} value={v}>
          {v}
        </option>
      ))}
    </select>
  );
}

function StringListInput({ value, onChange }: { value: unknown; onChange: (v: unknown) => void }) {
  const items = (value as string[] | undefined) ?? [];

  function setItem(index: number, v: string) {
    onChange(items.map((item, i) => (i === index ? v : item)));
  }

  function removeItem(index: number) {
    onChange(items.filter((_, i) => i !== index));
  }

  return (
    <div className="space-y-1">
      {items.map((item, index) => (
        <div key={index} className="flex gap-1">
          <Input value={item} onChange={(e) => setItem(index, e.target.value)} />
          <button
            type="button"
            onClick={() => removeItem(index)}
            className="text-slate-400 hover:text-red-600"
            aria-label="Remove value"
          >
            <Minus className="h-4 w-4" />
          </button>
        </div>
      ))}
      <button
        type="button"
        onClick={() => onChange([...items, ""])}
        className="inline-flex items-center gap-1 text-xs font-medium text-indigo-600 hover:text-indigo-700"
      >
        <Plus className="h-3 w-3" aria-hidden="true" /> Add value
      </button>
    </div>
  );
}

interface KeyValuePair {
  key: string;
  value: string;
}

function KeyValueInput({ value, onChange }: { value: unknown; onChange: (v: unknown) => void }) {
  const pairs = (value as KeyValuePair[] | undefined) ?? [];

  function setPair(index: number, patch: Partial<KeyValuePair>) {
    onChange(pairs.map((pair, i) => (i === index ? { ...pair, ...patch } : pair)));
  }

  function removePair(index: number) {
    onChange(pairs.filter((_, i) => i !== index));
  }

  return (
    <div className="space-y-1">
      {pairs.map((pair, index) => (
        <div key={index} className="flex gap-1">
          <Input
            value={pair.key}
            onChange={(e) => setPair(index, { key: e.target.value })}
            placeholder="key"
            className="max-w-[40%]"
          />
          <Input value={pair.value} onChange={(e) => setPair(index, { value: e.target.value })} placeholder="value" />
          <button
            type="button"
            onClick={() => removePair(index)}
            className="text-slate-400 hover:text-red-600"
            aria-label="Remove entry"
          >
            <Minus className="h-4 w-4" />
          </button>
        </div>
      ))}
      <button
        type="button"
        onClick={() => onChange([...pairs, { key: "", value: "" }])}
        className="inline-flex items-center gap-1 text-xs font-medium text-indigo-600 hover:text-indigo-700"
      >
        <Plus className="h-3 w-3" aria-hidden="true" /> Add entry
      </button>
    </div>
  );
}

// FieldInput renders the control for one FieldSchema, dispatching on its
// type. ResourceForm owns the surrounding label/help text; this only renders
// the control itself.
export default function FieldInput({ id, field, value, onChange }: FieldControlProps) {
  switch (field.type) {
    case "string":
      return <TextInput id={id} value={value} onChange={onChange} />;
    case "text":
      return <TextAreaInput id={id} value={value} onChange={onChange} />;
    case "number":
      return <NumberInput id={id} value={value} onChange={onChange} />;
    case "boolean":
      return <BooleanInput id={id} value={value} onChange={onChange} />;
    case "enum":
      return <EnumInput id={id} field={field} value={value} onChange={onChange} />;
    case "stringList":
      return <StringListInput value={value} onChange={onChange} />;
    case "keyValue":
      return <KeyValueInput value={value} onChange={onChange} />;
    case "reference":
      return <ReferenceField id={id} field={field} value={(value as string | undefined) ?? ""} onChange={onChange} />;
    case "group":
      return (
        <GroupField id={id} field={field} value={(value as Record<string, unknown>[] | undefined) ?? []} onChange={onChange} />
      );
  }
}
