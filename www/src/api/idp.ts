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

export interface UserSpec {
  username: string;
  password?: string;
  roles?: string[];
  disabled?: boolean;
}

export interface User {
  metadata: { uid: string; createdAt: string };
  spec: UserSpec;
}

// listUsers returns every user (admin only).
export async function listUsers(): Promise<User[]> {
  const out = await idpRequest<{ items: User[] }>("/user", { authenticated: true });
  return out.items;
}

// putUser creates or updates the user under uid (admin only). An empty
// spec.password on an update leaves the existing one untouched.
export async function putUser(uid: string, spec: UserSpec): Promise<User> {
  return idpRequest<User>(`/user/${encodeURIComponent(uid)}`, {
    method: "PUT",
    authenticated: true,
    body: { spec },
  });
}

// deleteUser removes the user (admin only).
export async function deleteUser(uid: string): Promise<void> {
  await idpRequest<void>(`/user/${encodeURIComponent(uid)}`, { method: "DELETE", authenticated: true });
}

export interface AccessTokenSpec {
  name: string;
  expiresAt?: string;
}

export interface AccessToken {
  metadata: { uid: string; createdAt: string };
  spec: AccessTokenSpec;
  status: { ownerUid?: string };
  token?: string;
}

// listAccessTokens returns every access token (admin only; never includes
// plaintext - only the create/update response ever does, once).
export async function listAccessTokens(): Promise<AccessToken[]> {
  const out = await idpRequest<{ items: AccessToken[] }>("/access_token", { authenticated: true });
  return out.items;
}

// putAccessToken creates or updates the access token under uid (admin only).
// The returned token field is only set when a new token was just generated.
export async function putAccessToken(uid: string, spec: AccessTokenSpec): Promise<AccessToken> {
  return idpRequest<AccessToken>(`/access_token/${encodeURIComponent(uid)}`, {
    method: "PUT",
    authenticated: true,
    body: { spec },
  });
}

// deleteAccessToken revokes the access token (admin only).
export async function deleteAccessToken(uid: string): Promise<void> {
  await idpRequest<void>(`/access_token/${encodeURIComponent(uid)}`, { method: "DELETE", authenticated: true });
}
