// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { afterEach, describe, expect, it, vi } from "vitest";

import { setToken } from "../auth";
import {
  deleteAccessToken,
  deleteUser,
  deviceVerify,
  listAccessTokens,
  listUsers,
  login,
  putAccessToken,
  putUser,
  userinfo,
} from "./idp";

describe("idp api", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    setToken("");
  });

  it("logs in with a POST body and returns the access token", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({ access_token: "a-token" }) });
    vi.stubGlobal("fetch", fetchMock);

    const token = await login("alice", "s3cr3t");

    expect(token).toBe("a-token");
    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe("/idp/login");
    expect(opts.method).toBe("POST");
    expect(opts.headers.Authorization).toBeUndefined();
    expect(opts.headers["Content-Type"]).toBe("application/json");
    expect(JSON.parse(opts.body as string)).toEqual({ username: "alice", password: "s3cr3t" });
  });

  it("resolves userinfo without a token when none is stored", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ subject: "alice", roles: ["admin"] }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const info = await userinfo();

    expect(info).toEqual({ subject: "alice", roles: ["admin"] });
    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe("/idp/userinfo");
    expect(opts.method).toBe("GET");
    expect(opts.headers.Authorization).toBeUndefined();
  });

  it("attaches a bearer token to an authenticated request when one is stored", async () => {
    setToken("a-token");
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({ subject: "alice", roles: [] }) });
    vi.stubGlobal("fetch", fetchMock);

    await userinfo();

    const [, opts] = fetchMock.mock.calls[0];
    expect(opts.headers.Authorization).toBe("Bearer a-token");
  });

  it("throws with the method, path and status on a non-2xx response", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: false, status: 401 });
    vi.stubGlobal("fetch", fetchMock);

    await expect(userinfo()).rejects.toThrow("GET /userinfo: 401");
  });

  it("returns undefined for a 204 No Content response", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 204 });
    vi.stubGlobal("fetch", fetchMock);

    await expect(deviceVerify("ABCD-1234", true)).resolves.toBeUndefined();
    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe("/idp/device/verify");
    expect(opts.method).toBe("POST");
    expect(JSON.parse(opts.body as string)).toEqual({ user_code: "ABCD-1234", approve: true });
  });

  it("lists users", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ items: [{ metadata: { uid: "alice", createdAt: "" }, spec: { username: "alice" } }] }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const users = await listUsers();

    expect(users).toHaveLength(1);
    expect(fetchMock.mock.calls[0][0]).toBe("/idp/user");
  });

  it("creates or updates a user", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ metadata: { uid: "alice", createdAt: "" }, spec: { username: "alice" } }),
    });
    vi.stubGlobal("fetch", fetchMock);

    await putUser("alice", { username: "alice", roles: ["admin"] });

    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe("/idp/user/alice");
    expect(opts.method).toBe("PUT");
    expect(JSON.parse(opts.body as string)).toEqual({ spec: { username: "alice", roles: ["admin"] } });
  });

  it("deletes a user", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 204 });
    vi.stubGlobal("fetch", fetchMock);

    await deleteUser("alice");

    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe("/idp/user/alice");
    expect(opts.method).toBe("DELETE");
  });

  it("lists access tokens", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ items: [{ metadata: { uid: "tok-1", createdAt: "" }, spec: { name: "ci" }, status: {} }] }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const tokens = await listAccessTokens();

    expect(tokens).toHaveLength(1);
    expect(fetchMock.mock.calls[0][0]).toBe("/idp/access_token");
  });

  it("creates or updates an access token", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ metadata: { uid: "tok-1", createdAt: "" }, spec: { name: "ci" }, status: {}, token: "infra_s3cr3t" }),
    });
    vi.stubGlobal("fetch", fetchMock);

    const out = await putAccessToken("tok-1", { name: "ci" });

    expect(out.token).toBe("infra_s3cr3t");
    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe("/idp/access_token/tok-1");
    expect(opts.method).toBe("PUT");
  });

  it("deletes an access token", async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 204 });
    vi.stubGlobal("fetch", fetchMock);

    await deleteAccessToken("tok-1");

    const [url, opts] = fetchMock.mock.calls[0];
    expect(url).toBe("/idp/access_token/tok-1");
    expect(opts.method).toBe("DELETE");
  });
});

describe("identity collection pagination", () => {
  afterEach(() => vi.restoreAllMocks());
  it("follows user pages", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ ok: true, status: 200, json: async () => ({ items: [{ metadata: { uid: "alice" } }], continue: "next" }) })
      .mockResolvedValueOnce({ ok: true, status: 200, json: async () => ({ items: [{ metadata: { uid: "bob" } }] }) });
    vi.stubGlobal("fetch", fetchMock);
    expect((await listUsers()).map((user) => user.metadata.uid)).toEqual(["alice", "bob"]);
    expect(fetchMock.mock.calls[1][0]).toBe("/idp/user?continue=next");
  });
});
