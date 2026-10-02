// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { type FormEvent, useState } from "react";
import { useSearchParams } from "react-router-dom";

import { deviceVerify } from "../api/idp";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";

type Outcome = "approved" | "denied";

export default function Device() {
  const [params] = useSearchParams();
  const [userCode, setUserCode] = useState(params.get("user_code") ?? "");
  const [error, setError] = useState("");
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function resolve(approve: boolean, e?: FormEvent) {
    e?.preventDefault();
    setError("");
    setSubmitting(true);
    try {
      await deviceVerify(userCode.trim(), approve);
      setOutcome(approve ? "approved" : "denied");
    } catch {
      setError("That code is invalid or has expired.");
    } finally {
      setSubmitting(false);
    }
  }

  if (outcome === "approved") {
    return (
      <section className="max-w-md">
        <h2 className="text-lg font-semibold text-slate-900">Device approved</h2>
        <p className="mt-2 text-sm text-slate-600">You can go back to your terminal.</p>
      </section>
    );
  }
  if (outcome === "denied") {
    return (
      <section className="max-w-md">
        <h2 className="text-lg font-semibold text-slate-900">Device login denied</h2>
      </section>
    );
  }

  return (
    <section className="max-w-md space-y-4">
      <div>
        <h2 className="text-lg font-semibold text-slate-900">Confirm device login</h2>
        <p className="mt-1 text-sm text-slate-600">
          Make sure this code matches the one shown in your terminal, then approve or deny it.
        </p>
      </div>
      <form onSubmit={(e) => resolve(true, e)} className="space-y-4">
        <div className="space-y-1">
          <Label htmlFor="user_code">Code</Label>
          <Input
            id="user_code"
            value={userCode}
            onChange={(e) => setUserCode(e.target.value.toUpperCase())}
            placeholder="XXXX-XXXX"
            required
          />
        </div>
        {error && <p className="text-sm text-red-600">{error}</p>}
        <div className="flex gap-2">
          <Button type="submit" disabled={submitting}>
            Approve
          </Button>
          <Button type="button" variant="outline" disabled={submitting} onClick={() => resolve(false)}>
            Deny
          </Button>
        </div>
      </form>
    </section>
  );
}
