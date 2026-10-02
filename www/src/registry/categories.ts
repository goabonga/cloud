// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { Building2, Globe, HardDrive, Network, Scale, Server, ShieldCheck } from "lucide-react";

import type { CategoryDef } from "./types";

// CATEGORIES lists the console's service groups in sidebar order, mirroring
// how the GCP console groups resources (VPC network, Compute, Storage, …).
export const CATEGORIES: CategoryDef[] = [
  { key: "resourceManagement", label: "Resource Manager", icon: Building2 },
  { key: "networking", label: "VPC network", icon: Network },
  { key: "security", label: "Security", icon: ShieldCheck },
  { key: "compute", label: "Compute", icon: Server },
  { key: "storage", label: "Storage", icon: HardDrive },
  { key: "loadbalancing", label: "Load balancing", icon: Scale },
  { key: "dns", label: "Network services", icon: Globe },
];
