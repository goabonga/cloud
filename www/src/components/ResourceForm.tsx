// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { type FormEvent, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";

import { createResource, getResource, type ResourceMetadataInput } from "../api/generic";
import { getPath, setPath } from "../lib/path";
import type { FieldSchema, ResourceDef } from "../registry";
import FieldInput from "./fields/FieldInput";
import ReferenceField from "./fields/ReferenceField";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { Label } from "./ui/label";

// PROJECT_FIELD is not part of any kind's spec - it drives the Project
// picker, which sets metadata.projectId instead of a spec field.
const PROJECT_FIELD: FieldSchema = { key: "projectId", label: "Project", type: "reference", referenceKind: "project" };

interface KeyValuePair {
  key: string;
  value: string;
}

// initialValues flattens a spec into the form's { [field.key]: value } shape,
// the inverse of assembleSpec. keyValue fields become { key, value } pairs so
// the list UI has something stable to edit.
function initialValues(fields: FieldSchema[], spec: Record<string, unknown>): Record<string, unknown> {
  const values: Record<string, unknown> = {};
  for (const field of fields) {
    if (field.type === "keyValue") {
      const map = (getPath(spec, field.key) as Record<string, string> | undefined) ?? {};
      values[field.key] = Object.entries(map).map(([key, value]): KeyValuePair => ({ key, value }));
    } else {
      values[field.key] = getPath(spec, field.key);
    }
  }
  return values;
}

// assembleSpec is the inverse of initialValues: it walks the form's flat
// values back into the nested spec shape the API expects, dropping fields
// left empty.
function assembleSpec(fields: FieldSchema[], values: Record<string, unknown>): Record<string, unknown> {
  let spec: Record<string, unknown> = {};
  for (const field of fields) {
    let value = values[field.key];
    if (field.type === "keyValue") {
      const pairs = (value as KeyValuePair[] | undefined) ?? [];
      const map = Object.fromEntries(pairs.filter((p) => p.key !== "").map((p) => [p.key, p.value]));
      value = Object.keys(map).length > 0 ? map : undefined;
    }
    if (value === undefined || value === "") continue;
    if (Array.isArray(value) && value.length === 0) continue;
    spec = setPath(spec, field.key, value);
  }
  return spec;
}

// Preserve fields outside the guided schema while allowing known fields to clear.
function preserveSpec(original: Record<string, unknown>, fields: FieldSchema[], values: Record<string, unknown>): Record<string, unknown> {
  let result = structuredClone(original);
  for (const field of fields) result = setPath(result, field.key, undefined);
  const guided = assembleSpec(fields, values);
  for (const field of fields) result = setPath(result, field.key, getPath(guided, field.key));
  return result;
}

// ResourceForm is the schema-driven create/edit form for one resource kind.
// With no uid it creates a new resource; with one it loads and edits it. An
// "Advanced" raw-JSON mode covers anything the schema doesn't model yet.
export default function ResourceForm({ def, uid }: { def: ResourceDef; uid?: string }) {
  const navigate = useNavigate();
  const isEdit = uid !== undefined;
  const [newUid, setNewUid] = useState("");
  const [projectId, setProjectId] = useState("");
  const [originalSpec, setOriginalSpec] = useState<Record<string, unknown>>({});
  const [metadata, setMetadata] = useState<ResourceMetadataInput>({});
  const [values, setValues] = useState<Record<string, unknown>>({});
  const [loading, setLoading] = useState(isEdit);
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [useAdvanced, setUseAdvanced] = useState(false);
  const [advancedText, setAdvancedText] = useState("{}");

  useEffect(() => {
    if (uid === undefined) return;
    setLoading(true);
    setError("");
    getResource(def.kind, uid)
      .then((resource) => {
        setOriginalSpec(resource.spec);
        setMetadata(resource.metadata);
        setProjectId(resource.metadata.projectId ?? "");
        setValues(initialValues(def.fields, resource.spec));
        setAdvancedText(JSON.stringify(resource.spec, null, 2));
      })
      .catch(() => setError("Could not load this resource."))
      .finally(() => setLoading(false));
  }, [def, uid]);

  function toggleAdvanced() {
    if (useAdvanced) {
      try {
        const parsed = JSON.parse(advancedText) as Record<string, unknown>;
        setOriginalSpec(parsed);
        setValues(initialValues(def.fields, parsed));
      } catch {
        // Leave the structured values as they were if the JSON is invalid.
      }
    } else {
      setAdvancedText(JSON.stringify(preserveSpec(originalSpec, def.fields, values), null, 2));
    }
    setUseAdvanced((v) => !v);
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError("");

    let spec: Record<string, unknown>;
    if (useAdvanced) {
      try {
        spec = JSON.parse(advancedText) as Record<string, unknown>;
      } catch {
        setError("spec is not valid JSON");
        return;
      }
    } else {
      spec = preserveSpec(originalSpec, def.fields, values);
    }

    const targetUid = uid ?? newUid;
    setSubmitting(true);
    try {
      if (isEdit) {
        await createResource(def.kind, targetUid, spec, metadata);
      } else if (def.scoped !== false && projectId) {
        await createResource(def.kind, targetUid, spec, { projectId });
      } else {
        await createResource(def.kind, targetUid, spec);
      }
      navigate(`/${def.kind}`);
    } catch (e) {
      setError(String(e));
    } finally {
      setSubmitting(false);
    }
  }

  if (loading) {
    return <p className="text-sm text-slate-500">Loading…</p>;
  }

  return (
    <section className="max-w-2xl space-y-4">
      <h2 className="text-lg font-semibold text-slate-900">
        {isEdit ? `Edit ${def.label}` : `Create ${def.label}`}
      </h2>
      {error && <p className="text-sm text-red-600">{error}</p>}
      <form onSubmit={onSubmit} className="space-y-4 rounded-lg border border-slate-200 bg-white p-4">
        <div className="space-y-1">
          <Label htmlFor="resource-uid">UID</Label>
          {isEdit ? (
            <p className="text-sm text-slate-900">{uid}</p>
          ) : (
            <Input
              id="resource-uid"
              value={newUid}
              onChange={(e) => setNewUid(e.target.value)}
              placeholder={`${def.kind}-1`}
              required
            />
          )}
        </div>

        {!isEdit && def.scoped !== false && (
          <div className="space-y-1">
            <Label htmlFor="field-projectId">Project</Label>
            <ReferenceField id="field-projectId" field={PROJECT_FIELD} value={projectId} onChange={setProjectId} />
            <p className="text-xs text-slate-500">
              Leave blank to create it unscoped, visible only to you. Project scope cannot be changed later.
            </p>
          </div>
        )}

        {!useAdvanced &&
          def.fields.map((field) => (
            <div key={field.key} className="space-y-1">
              <Label htmlFor={`field-${field.key}`}>
                {field.label}
                {field.required && <span className="text-red-600"> *</span>}
              </Label>
              <FieldInput
                id={`field-${field.key}`}
                field={field}
                value={values[field.key]}
                onChange={(v) => setValues((prev) => ({ ...prev, [field.key]: v }))}
              />
              {field.helpText && <p className="text-xs text-slate-500">{field.helpText}</p>}
            </div>
          ))}

        {useAdvanced && (
          <div className="space-y-1">
            <Label htmlFor="resource-advanced">Spec (JSON)</Label>
            <textarea
              id="resource-advanced"
              className="w-full rounded-md border border-slate-300 bg-white px-3 py-1.5 font-mono text-xs shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500"
              rows={10}
              value={advancedText}
              onChange={(e) => setAdvancedText(e.target.value)}
            />
          </div>
        )}

        <div className="flex items-center justify-between">
          <button
            type="button"
            onClick={toggleAdvanced}
            className="text-xs font-medium text-indigo-600 hover:text-indigo-700"
          >
            {useAdvanced ? "Use the guided form" : "Advanced: edit raw JSON"}
          </button>
          <div className="flex gap-2">
            <Button type="button" variant="outline" onClick={() => navigate(`/${def.kind}`)}>
              Cancel
            </Button>
            <Button type="submit" disabled={submitting}>
              {isEdit ? "Save" : "Create"}
            </Button>
          </div>
        </div>
      </form>
    </section>
  );
}
