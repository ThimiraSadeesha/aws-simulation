"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError, setCsrfToken, setUnauthorizedHandler } from "@/lib/api/client";
import type { SessionResponse, User } from "@/lib/types";

type Status = "loading" | "authenticated" | "unauthenticated" | "error";

interface AuthContextValue {
  user: User | null;
  csrfToken: string | null;
  status: Status;
  error: string | null;
  login: (username: string, password: string) => Promise<User>;
  register: (username: string, password: string, email?: string) => Promise<User>;
  logout: () => Promise<void>;
  refresh: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export const PUBLIC_PATHS = ["/login", "/register"];

export function loginRedirectPath(pathname: string | null): string {
  if (!pathname || PUBLIC_PATHS.includes(pathname) || pathname === "/") return "/login";
  return `/login?next=${encodeURIComponent(pathname)}`;
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [csrf, setCsrf] = useState<string | null>(null);
  const [status, setStatus] = useState<Status>("loading");
  const [error, setError] = useState<string | null>(null);
  const router = useRouter();
  const pathname = usePathname();
  const pathRef = useRef(pathname);
  const queryClient = useQueryClient();

  useEffect(() => {
    pathRef.current = pathname;
  }, [pathname]);

  const applySession = useCallback((s: SessionResponse) => {
    setCsrfToken(s.csrf_token);
    setCsrf(s.csrf_token);
    setUser(s.user);
    setStatus("authenticated");
    setError(null);
  }, []);

  const clearSession = useCallback(() => {
    setCsrfToken(null);
    setCsrf(null);
    setUser(null);
    setStatus("unauthenticated");
  }, []);

  const refresh = useCallback(
    () =>
      api<SessionResponse>("/auth/me", { skipAuthRedirect: true }).then(applySession, (err: unknown) => {
        if (err instanceof ApiError && err.status === 401) {
          clearSession();
        } else {
          setStatus("error");
          setError(err instanceof Error ? err.message : String(err));
        }
      }),
    [applySession, clearSession],
  );

  // Recover the session (and CSRF token) after a reload.
  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Any 401 from the API (expired session) → drop the session and go to /login.
  useEffect(() => {
    setUnauthorizedHandler(() => {
      clearSession();
      queryClient.clear();
      const p = pathRef.current;
      if (!p || !PUBLIC_PATHS.includes(p)) router.replace(loginRedirectPath(p));
    });
    return () => setUnauthorizedHandler(null);
  }, [clearSession, queryClient, router]);

  const login = useCallback(
    async (username: string, password: string) => {
      const s = await api<SessionResponse>("/auth/login", {
        method: "POST",
        body: { username, password },
        skipAuthRedirect: true,
      });
      queryClient.clear();
      applySession(s);
      return s.user;
    },
    [applySession, queryClient],
  );

  const register = useCallback(async (username: string, password: string, email?: string) => {
    const r = await api<{ user: User }>("/auth/register", {
      method: "POST",
      body: email ? { username, password, email } : { username, password },
      skipAuthRedirect: true,
    });
    return r.user;
  }, []);

  const logout = useCallback(async () => {
    try {
      await api<void>("/auth/logout", { method: "POST", skipAuthRedirect: true });
    } finally {
      clearSession();
      queryClient.clear();
      router.replace("/login");
    }
  }, [clearSession, queryClient, router]);

  const value = useMemo<AuthContextValue>(
    () => ({ user, csrfToken: csrf, status, error, login, register, logout, refresh }),
    [user, csrf, status, error, login, register, logout, refresh],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
