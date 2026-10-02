// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { useAuth } from "../auth-context";

export default function Profile() {
  const { subject, roles, loading } = useAuth();

  return (
    <section className="max-w-lg space-y-4">
      <h2 className="text-lg font-semibold text-slate-900">Profile</h2>
      {!loading && !subject && <p className="text-sm text-red-600">Could not load your profile.</p>}
      {subject && (
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 rounded-lg border border-slate-200 bg-white p-4 text-sm">
          <dt className="font-medium text-slate-500">Username</dt>
          <dd className="text-slate-900">{subject}</dd>
          <dt className="font-medium text-slate-500">Roles</dt>
          <dd className="text-slate-900">{roles.length > 0 ? roles.join(", ") : "none"}</dd>
        </dl>
      )}
    </section>
  );
}
