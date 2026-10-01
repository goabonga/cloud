// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { Minus, Plus } from "lucide-react";

import type { FieldSchema } from "../../registry/types";
import FieldInput from "./FieldInput";

// GroupField renders a repeatable sub-form: one row of inputs per item in
// value, each built from field.fields, with add/remove controls. Used for
// e.g. ComputeSpec.Disks or ACLPolicySpec.Rules.
export default function GroupField({
  id,
  field,
  value,
  onChange,
}: {
  id: string;
  field: FieldSchema;
  value: Record<string, unknown>[];
  onChange: (value: Record<string, unknown>[]) => void;
}) {
  const subFields = field.fields ?? [];

  function updateRow(index: number, key: string, v: unknown) {
    onChange(value.map((row, i) => (i === index ? { ...row, [key]: v } : row)));
  }

  function removeRow(index: number) {
    onChange(value.filter((_, i) => i !== index));
  }

  function addRow() {
    onChange([...value, {}]);
  }

  return (
    <div className="space-y-2" id={id}>
      {value.map((row, index) => (
        <div key={index} className="flex flex-wrap items-end gap-2 rounded-md border border-slate-200 p-2">
          {subFields.map((sub) => (
            <div key={sub.key} className="space-y-1">
              <label className="block text-xs font-medium text-slate-500" htmlFor={`${id}-${index}-${sub.key}`}>
                {sub.label}
              </label>
              <FieldInput
                id={`${id}-${index}-${sub.key}`}
                field={sub}
                value={row[sub.key]}
                onChange={(v) => updateRow(index, sub.key, v)}
              />
            </div>
          ))}
          <button
            type="button"
            onClick={() => removeRow(index)}
            className="mb-1 text-slate-400 hover:text-red-600"
            aria-label={`Remove row ${index + 1}`}
          >
            <Minus className="h-4 w-4" aria-hidden="true" />
          </button>
        </div>
      ))}
      <button
        type="button"
        onClick={addRow}
        className="inline-flex items-center gap-1 text-xs font-medium text-indigo-600 hover:text-indigo-700"
      >
        <Plus className="h-3 w-3" aria-hidden="true" /> Add {field.label.toLowerCase()}
      </button>
    </div>
  );
}
