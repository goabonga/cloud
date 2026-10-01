// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { type FormEvent, useEffect, useState } from "react";

import { type User, deleteUser, listUsers, putUser } from "../../api/idp";
import { useAuth } from "../../auth-context";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { Label } from "../../components/ui/label";

function parseRoles(input: string): string[] {
  return input
    .split(",")
    .map((r) => r.trim())
    .filter(Boolean);
}

export default function Users() {
  const { roles: myRoles, subject: mySubject } = useAuth();
  const [users, setUsers] = useState<User[]>([]);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<string | null>(null);

  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [roleInput, setRoleInput] = useState("");

  const isAdmin = myRoles.includes("admin");

  function reload() {
    listUsers()
      .then(setUsers)
      .catch(() => setError("Could not load users."));
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
      await putUser(username, { username, password, roles: parseRoles(roleInput) });
      setUsername("");
      setPassword("");
      setRoleInput("");
      reload();
    } catch {
      setError("Could not create the user.");
    }
  }

  async function onDelete(uid: string) {
    if (!window.confirm(`Delete user "${uid}"? This cannot be undone.`)) {
      return;
    }
    try {
      await deleteUser(uid);
      reload();
    } catch {
      setError("Could not delete the user.");
    }
  }

  async function onSaveEdit(uid: string, rolesText: string, disabled: boolean, newPassword: string) {
    try {
      await putUser(uid, { username: uid, roles: parseRoles(rolesText), disabled, password: newPassword || undefined });
      setEditing(null);
      reload();
    } catch {
      setError("Could not update the user.");
    }
  }

  return (
    <section className="max-w-3xl space-y-6">
      <h2 className="text-lg font-semibold text-slate-900">Users</h2>
      {error && <p className="text-sm text-red-600">{error}</p>}

      <form onSubmit={onCreate} className="flex flex-wrap items-end gap-3 rounded-lg border border-slate-200 bg-white p-4">
        <div className="space-y-1">
          <Label htmlFor="new-username">Username</Label>
          <Input id="new-username" value={username} onChange={(e) => setUsername(e.target.value)} required />
        </div>
        <div className="space-y-1">
          <Label htmlFor="new-password">Password</Label>
          <Input
            id="new-password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="new-roles">Roles (comma-separated)</Label>
          <Input id="new-roles" value={roleInput} onChange={(e) => setRoleInput(e.target.value)} placeholder="admin" />
        </div>
        <Button type="submit">Create user</Button>
      </form>

      <table className="w-full border-collapse text-sm">
        <thead>
          <tr className="border-b border-slate-200 text-left text-slate-500">
            <th className="py-2">Username</th>
            <th className="py-2">Roles</th>
            <th className="py-2">Status</th>
            <th className="py-2">Created</th>
            <th className="py-2" />
          </tr>
        </thead>
        <tbody>
          {users.map((u) => (
            <UserRow
              key={u.metadata.uid}
              user={u}
              isSelf={u.metadata.uid === mySubject}
              editing={editing === u.metadata.uid}
              onEdit={() => setEditing(u.metadata.uid)}
              onCancelEdit={() => setEditing(null)}
              onSaveEdit={(rolesText, disabled, newPassword) => onSaveEdit(u.metadata.uid, rolesText, disabled, newPassword)}
              onDelete={() => onDelete(u.metadata.uid)}
            />
          ))}
        </tbody>
      </table>
    </section>
  );
}

function UserRow({
  user,
  isSelf,
  editing,
  onEdit,
  onCancelEdit,
  onSaveEdit,
  onDelete,
}: {
  user: User;
  isSelf: boolean;
  editing: boolean;
  onEdit: () => void;
  onCancelEdit: () => void;
  onSaveEdit: (rolesText: string, disabled: boolean, newPassword: string) => void;
  onDelete: () => void;
}) {
  const [rolesText, setRolesText] = useState((user.spec.roles ?? []).join(", "));
  const [disabled, setDisabled] = useState(Boolean(user.spec.disabled));
  const [newPassword, setNewPassword] = useState("");

  if (editing) {
    return (
      <tr className="border-b border-slate-100">
        <td className="py-2 font-medium text-slate-900">{user.metadata.uid}</td>
        <td className="py-2">
          <Input value={rolesText} onChange={(e) => setRolesText(e.target.value)} className="h-8" />
        </td>
        <td className="py-2">
          <label className="flex items-center gap-2 text-slate-700">
            <input type="checkbox" checked={disabled} onChange={(e) => setDisabled(e.target.checked)} />
            Disabled
          </label>
        </td>
        <td className="py-2" colSpan={1}>
          <Input
            type="password"
            placeholder="new password (optional)"
            value={newPassword}
            onChange={(e) => setNewPassword(e.target.value)}
            className="h-8"
          />
        </td>
        <td className="space-x-2 py-2 text-right">
          <Button size="sm" onClick={() => onSaveEdit(rolesText, disabled, newPassword)}>
            Save
          </Button>
          <Button size="sm" variant="outline" onClick={onCancelEdit}>
            Cancel
          </Button>
        </td>
      </tr>
    );
  }

  return (
    <tr className="border-b border-slate-100">
      <td className="py-2 font-medium text-slate-900">{user.metadata.uid}</td>
      <td className="py-2 text-slate-600">{(user.spec.roles ?? []).join(", ") || "none"}</td>
      <td className="py-2 text-slate-600">{user.spec.disabled ? "Disabled" : "Active"}</td>
      <td className="py-2 text-slate-600">{new Date(user.metadata.createdAt).toLocaleString()}</td>
      <td className="space-x-2 py-2 text-right">
        <Button size="sm" variant="outline" onClick={onEdit}>
          Edit
        </Button>
        <Button
          size="sm"
          variant="outline"
          onClick={onDelete}
          disabled={isSelf}
          title={isSelf ? "You can't delete your own account from here" : undefined}
        >
          Delete
        </Button>
      </td>
    </tr>
  );
}
