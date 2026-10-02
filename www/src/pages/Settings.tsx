// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { type FormEvent, useState } from "react";

import { getToken, setToken } from "../auth";

export default function Settings() {
  const [token, setTokenValue] = useState(getToken());
  const [saved, setSaved] = useState(false);

  function onSave(e: FormEvent) {
    e.preventDefault();
    setToken(token.trim());
    setSaved(true);
  }

  return (
    <section>
      <h2>Settings</h2>
      <p>
        Advanced: override the bearer token the dashboard uses to talk to the control plane, e.g. to use a static
        machine token instead of your own signed-in session. This is stored in your browser only.
      </p>
      <form className="row" onSubmit={onSave}>
        <label>
          API token
          <br />
          <input
            style={{ width: "24rem" }}
            value={token}
            onChange={(e) => {
              setTokenValue(e.target.value);
              setSaved(false);
            }}
            placeholder="eyJhbGciOiJFUzI1NiIs..."
          />
        </label>
        <button type="submit">Save</button>
      </form>
      {saved && <p>Saved.</p>}
    </section>
  );
}
