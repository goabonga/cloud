// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { getToken } from "../auth";

export interface UserInfo {
  subject: string;
  roles: string[];
}

async function idpRequest<T>(path: string, opts: { method?: string; body?: unknown; authenticated?: boolean } = {}): Promise<T> {
  const headers: Record<string, string> = {};
  if (opts.authenticated) {
    const token = getToken();
    if (token) {
      headers["Authorization"] = `Bearer ${token}`;
    }
  }
  if (opts.body !== undefined) {
    headers["Content-Type"] = "application/json";
  }
  const resp = await fetch(`/idp${path}`, {
    method: opts.method ?? "GET",
    headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
  });
  if (!resp.ok) {
    throw new Error(`${opts.method ?? "GET"} ${path}: ${resp.status}`);
  }
  if (resp.status === 204) {
    return undefined as T;
  }
  return (await resp.json()) as T;
}

// login exchanges a username and password for a bearer token carrying the
// user's roles, or throws if the credentials are rejected.
export async function login(username: string, password: string): Promise<string> {
  const out = await idpRequest<{ access_token: string }>("/login", {
    method: "POST",
    body: { username, password },
  });
  return out.access_token;
}

// userinfo resolves the caller's own identity from the stored bearer token.
export async function userinfo(): Promise<UserInfo> {
  return idpRequest<UserInfo>("/userinfo", { authenticated: true });
}

// deviceVerify approves or denies a pending device authorization on behalf of
// the signed-in caller.
export async function deviceVerify(userCode: string, approve: boolean): Promise<void> {
  await idpRequest<void>("/device/verify", {
    method: "POST",
    authenticated: true,
    body: { user_code: userCode, approve },
  });
}
