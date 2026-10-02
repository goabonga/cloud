// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { apiRequest } from "./http";

// KINDS are the control-plane resource kinds the dashboard can browse. Secret
// and SSL CA are excluded: they need the encryption key and have dedicated flows.
export const KINDS = [
  "vpc",
  "subnet",
  "security_group",
  "security_group_rule",
  "ip_address",
  "igw",
  "route",
  "kms_keyring",
  "kms_key",
  "disk",
  "disk_file",
  "compute",
  "microvm",
  "acl_policy",
  "dns_zone",
  "dns_record",
  "peering",
  "load_balancer",
  "lb_backend",
  "lb_target_group",
  "lb_target",
  "lb_listener",
  "waf_policy",
  "waf_rule",
  "node",
  "node_pool",
  "organization",
  "folder",
  "project",
  "iam_binding",
] as const;

export interface GenericResource {
  metadata: {
    uid: string;
    name?: string;
    generation: number;
    createdAt: string;
    projectId?: string;
    ownerUid?: string;
  };
  spec: Record<string, unknown>;
  status: { phase?: string } & Record<string, unknown>;
}

export interface ResourceMetadataInput {
  name?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  organizationId?: string;
  projectId?: string;
}

interface List {
  items: GenericResource[];
  continue?: string;
}

export async function listResources(kind: string): Promise<GenericResource[]> {
  const items: GenericResource[] = [];
  const seen = new Set<string>();
  let next = "";
  do {
    const path = next ? `/${kind}?continue=${encodeURIComponent(next)}` : `/${kind}`;
    const out = await apiRequest<List>("GET", path);
    items.push(...(out.items ?? []));
    next = out.continue ?? "";
    if (next && seen.has(next)) throw new Error("Repeated continuation token");
    seen.add(next);
  } while (next);
  return items;
}

export async function getResource(kind: string, uid: string): Promise<GenericResource> {
  return apiRequest<GenericResource>("GET", `/${kind}/${uid}`);
}

export async function createResource(
  kind: string,
  uid: string,
  spec: Record<string, unknown>,
  metadata?: ResourceMetadataInput,
): Promise<GenericResource> {
  return apiRequest<GenericResource>("PUT", `/${kind}/${uid}`, metadata ? { spec, metadata } : { spec });
}

export async function deleteResource(kind: string, uid: string): Promise<void> {
  await apiRequest<void>("DELETE", `/${kind}/${uid}`);
}
