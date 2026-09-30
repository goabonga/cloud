// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { useEffect, useState } from "react";

import { type UserInfo, userinfo } from "../api/idp";

export default function Profile() {
  const [info, setInfo] = useState<UserInfo | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    userinfo()
      .then(setInfo)
      .catch(() => setError("Could not load your profile."));
  }, []);

  return (
    <section className="max-w-lg space-y-4">
      <h2 className="text-lg font-semibold text-slate-900">Profile</h2>
      {error && <p className="text-sm text-red-600">{error}</p>}
      {info && (
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 rounded-lg border border-slate-200 bg-white p-4 text-sm">
          <dt className="font-medium text-slate-500">Username</dt>
          <dd className="text-slate-900">{info.subject}</dd>
          <dt className="font-medium text-slate-500">Roles</dt>
          <dd className="text-slate-900">{info.roles.length > 0 ? info.roles.join(", ") : "none"}</dd>
        </dl>
      )}
    </section>
  );
}
