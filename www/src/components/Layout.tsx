// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { Navigate, NavLink, Outlet, useNavigate } from "react-router-dom";

import { AuthProvider, useAuth } from "../auth-context";
import { getToken, setToken } from "../auth";
import { Avatar, AvatarFallback } from "./ui/avatar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu";

function navClass({ isActive }: { isActive: boolean }): string {
  return [
    "block rounded-md px-3 py-2 text-sm font-medium transition-colors",
    isActive ? "bg-slate-800 text-white" : "text-slate-300 hover:bg-slate-800 hover:text-white",
  ].join(" ");
}

export default function Layout() {
  const token = getToken();
  if (!token) {
    return <Navigate to="/login" replace />;
  }

  return (
    <AuthProvider>
      <LayoutShell />
    </AuthProvider>
  );
}

function LayoutShell() {
  const navigate = useNavigate();
  const { subject, roles } = useAuth();

  function signOut() {
    setToken("");
    navigate("/login", { replace: true });
  }

  return (
    <div className="flex h-screen">
      <aside className="flex w-56 flex-col bg-slate-900 p-4">
        <h1 className="mb-6 px-3 text-lg font-semibold text-white">infra</h1>
        <nav className="space-y-1">
          <NavLink to="/" end className={navClass}>
            Overview
          </NavLink>
          <NavLink to="/vpcs" className={navClass}>
            VPCs
          </NavLink>
          <NavLink to="/resources" className={navClass}>
            Resources
          </NavLink>
          <NavLink to="/acls" className={navClass}>
            ACL policies
          </NavLink>
          <NavLink to="/settings" className={navClass}>
            Settings
          </NavLink>
          {roles.includes("admin") && (
            <>
              <p className="px-3 pt-4 pb-1 text-xs font-semibold tracking-wide text-slate-500 uppercase">IAM</p>
              <NavLink to="/iam" end className={navClass}>
                Users
              </NavLink>
              <NavLink to="/iam/tokens" className={navClass}>
                Access tokens
              </NavLink>
            </>
          )}
        </nav>
      </aside>
      <div className="flex flex-1 flex-col overflow-hidden">
        <header className="flex h-14 shrink-0 items-center justify-end border-b border-slate-200 bg-white px-4">
          <DropdownMenu>
            <DropdownMenuTrigger className="rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-indigo-500">
              <Avatar title={subject}>
                <AvatarFallback>{(subject || "?").slice(0, 1).toUpperCase()}</AvatarFallback>
              </Avatar>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuLabel>Account</DropdownMenuLabel>
              <DropdownMenuItem asChild>
                <NavLink to="/profile">Profile</NavLink>
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={signOut}>Sign out</DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </header>
        <main className="flex-1 overflow-auto p-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
