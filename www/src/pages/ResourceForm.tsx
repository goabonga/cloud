// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { useParams } from "react-router-dom";

import ResourceForm from "../components/ResourceForm";
import { RESOURCES } from "../registry";

// ResourceFormPage is the generic create/edit page for every resource kind:
// :uid present means edit, absent means create.
export default function ResourceFormPage() {
  const { kind = "", uid } = useParams();
  const def = RESOURCES[kind];

  if (!def) {
    return <p className="text-sm text-red-600">Unknown resource kind &quot;{kind}&quot;.</p>;
  }

  return <ResourceForm def={def} uid={uid} key={`${def.kind}:${uid ?? "new"}`} />;
}
