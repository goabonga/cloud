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
  "waf_policy",
  "waf_rule",
  "node",
  "node_pool",
] as const;

export interface GenericResource {
  metadata: { uid: string; name?: string; generation: number; createdAt: string };
  spec: Record<string, unknown>;
  status: { phase?: string } & Record<string, unknown>;
}

interface List {
  items: GenericResource[];
}

export async function listResources(kind: string): Promise<GenericResource[]> {
  return (await apiRequest<List>("GET", `/${kind}`)).items ?? [];
}

export async function getResource(kind: string, uid: string): Promise<GenericResource> {
  return apiRequest<GenericResource>("GET", `/${kind}/${uid}`);
}

export async function createResource(
  kind: string,
  uid: string,
  spec: Record<string, unknown>,
): Promise<GenericResource> {
  return apiRequest<GenericResource>("PUT", `/${kind}/${uid}`, { spec });
}

export async function deleteResource(kind: string, uid: string): Promise<void> {
  await apiRequest<void>("DELETE", `/${kind}/${uid}`);
}
