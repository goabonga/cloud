// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Chris <goabonga@pm.me>

import { type ReactNode, createContext, useContext, useEffect, useState } from "react";

import { userinfo } from "./api/idp";

interface AuthState {
  subject: string;
  roles: string[];
  loading: boolean;
}

const AuthContext = createContext<AuthState>({ subject: "", roles: [], loading: true });

// AuthProvider fetches the signed-in user's identity once and makes it
// available to every descendant via useAuth(), instead of each page
// re-fetching /userinfo for itself.
export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({ subject: "", roles: [], loading: true });

  useEffect(() => {
    let cancelled = false;
    userinfo()
      .then((info) => {
        if (!cancelled) {
          setState({ subject: info.subject, roles: info.roles, loading: false });
        }
      })
      .catch(() => {
        if (!cancelled) {
          setState({ subject: "", roles: [], loading: false });
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return <AuthContext.Provider value={state}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  return useContext(AuthContext);
}
