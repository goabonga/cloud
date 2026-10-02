// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

const TOKEN_KEY = "infra.token";

// getToken returns the stored API bearer token, or an empty string.
export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) ?? "";
}

// setToken stores (or clears, when empty) the API bearer token.
export function setToken(token: string): void {
  if (token) {
    localStorage.setItem(TOKEN_KEY, token);
  } else {
    localStorage.removeItem(TOKEN_KEY);
  }
}
