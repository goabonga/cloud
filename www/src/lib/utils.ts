// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";

// cn merges conditional class names and resolves conflicting Tailwind
// utilities, so a caller can override a component's default classes.
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
