// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { type FormEvent, useEffect, useState } from "react";

import { type AccessToken, deleteAccessToken, listAccessTokens, putAccessToken } from "../../api/idp";
import { useAuth } from "../../auth-context";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { Label } from "../../components/ui/label";

export default function AccessTokens() {
  const { roles } = useAuth();
  const isAdmin = roles.includes("admin");
  const [tokens, setTokens] = useState<AccessToken[]>([]);
  const [error, setError] = useState("");
  const [revealed, setRevealed] = useState("");

  const [name, setName] = useState("");
  const [expiresAt, setExpiresAt] = useState("");

  function reload() {
    listAccessTokens()
      .then(setTokens)
      .catch(() => setError("Could not load access tokens."));
  }

  useEffect(() => {
    if (isAdmin) {
      reload();
    }
  }, [isAdmin]);

  if (!isAdmin) {
    return <p className="text-sm text-slate-600">You need the admin role to view this page.</p>;
  }

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    setError("");
    try {
      const out = await putAccessToken(crypto.randomUUID(), {
        name,
        expiresAt: expiresAt ? new Date(expiresAt).toISOString() : undefined,
      });
      setRevealed(out.token ?? "");
      setName("");
      setExpiresAt("");
      reload();
    } catch {
      setError("Could not create the access token.");
    }
  }

  async function onRevoke(uid: string) {
    if (!window.confirm("Revoke this access token? Anything using it will stop working immediately.")) {
      return;
    }
    try {
      await deleteAccessToken(uid);
      reload();
    } catch {
      setError("Could not revoke the access token.");
    }
  }

  return (
    <section className="max-w-3xl space-y-6">
      <h2 className="text-lg font-semibold text-slate-900">Access tokens</h2>
      {error && <p className="text-sm text-red-600">{error}</p>}

      {revealed && (
        <div className="space-y-2 rounded-lg border border-amber-300 bg-amber-50 p-4">
          <p className="text-sm font-medium text-amber-900">
            Copy this token now - it won&apos;t be shown again.
          </p>
          <div className="flex items-center gap-2">
            <code className="flex-1 overflow-x-auto rounded bg-white px-2 py-1 text-xs">{revealed}</code>
            <Button
              type="button"
              size="sm"
              onClick={() => navigator.clipboard.writeText(revealed)}
            >
              Copy
            </Button>
            <Button type="button" size="sm" variant="outline" onClick={() => setRevealed("")}>
              Dismiss
            </Button>
          </div>
        </div>
      )}

      <form onSubmit={onCreate} className="flex flex-wrap items-end gap-3 rounded-lg border border-slate-200 bg-white p-4">
        <div className="space-y-1">
          <Label htmlFor="token-name">Name</Label>
          <Input id="token-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="terraform" required />
        </div>
        <div className="space-y-1">
          <Label htmlFor="token-expires">Expires (optional)</Label>
          <Input id="token-expires" type="date" value={expiresAt} onChange={(e) => setExpiresAt(e.target.value)} />
        </div>
        <Button type="submit">Create token</Button>
      </form>

      <table className="w-full border-collapse text-sm">
        <thead>
          <tr className="border-b border-slate-200 text-left text-slate-500">
            <th className="py-2">Name</th>
            <th className="py-2">Owner</th>
            <th className="py-2">Created</th>
            <th className="py-2">Expires</th>
            <th className="py-2" />
          </tr>
        </thead>
        <tbody>
          {tokens.map((t) => (
            <tr key={t.metadata.uid} className="border-b border-slate-100">
              <td className="py-2 font-medium text-slate-900">{t.spec.name}</td>
              <td className="py-2 text-slate-600">{t.status.ownerUid}</td>
              <td className="py-2 text-slate-600">{new Date(t.metadata.createdAt).toLocaleString()}</td>
              <td className="py-2 text-slate-600">
                {t.spec.expiresAt ? new Date(t.spec.expiresAt).toLocaleDateString() : "never"}
              </td>
              <td className="py-2 text-right">
                <Button size="sm" variant="outline" onClick={() => onRevoke(t.metadata.uid)}>
                  Revoke
                </Button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
