"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { loginFailure } from "@/lib/auth/loginFailure";
import { authClient } from "@/lib/connect/client";
import {
  classifySessionError,
  sessionRetryDelayMs,
  type SessionMiss,
} from "@/lib/session/check";
import { onSessionEnded } from "@/lib/session/ended";
import {
  refreshSession,
  signIn,
  signOut,
  switchSessionTenant,
} from "@/lib/session/client";
import { userFromWhoAmI, type AuthUser } from "@/lib/session/user";

export type { AuthUser } from "@/lib/session/user";

/**
 * AuthContext — single source of truth for the signed-in user on the client.
 *
 * The session lives in the session role (ADR-0048): an httpOnly cookie the
 * page never reads, and the tokens behind it, which the page never holds. On
 * mount the context asks the role to keep the session alive and, when there is
 * one, reads the user with WhoAmI through the session transport. Sign-in, the
 * tenant switch and sign-out are the role's own endpoints; each is followed by
 * WhoAmI, which is what names the user and their tenant's slug.
 *
 * A role that cannot check the session — the keys that verify it would not
 * load, IAM restarting — leaves the status "unavailable", not signed out: the
 * context asks again with backoff (sessionRetryDelayMs) until the role
 * answers either way.
 */

export type AuthStatus =
  "loading" | "authenticated" | "unauthenticated" | "unavailable";

type AuthContextValue = {
  user: AuthUser | null;
  status: AuthStatus;
  error: string | null;
  login: (subject: string, password: string) => Promise<void>;
  // switchTenant re-scopes the session to another tenant the subject is a
  // member of. Throws when the role refuses (not a member, or disabled).
  switchTenant: (tenantId: string) => Promise<void>;
  logout: () => Promise<void>;
};

const AuthContext = createContext<AuthContextValue | null>(null);

async function currentUser(): Promise<AuthUser> {
  return userFromWhoAmI(await authClient.whoAmI({}));
}

// Module-level dedup for the on-mount rehydrate: StrictMode mounts effects
// twice in dev, across two provider instances, and both would otherwise ask
// the role to rotate at once.
let rehydratePromise: Promise<Rehydrated> | null = null;

/** What the load-time check found: the user, or why there is none. */
type Rehydrated = { kind: "signed-in"; user: AuthUser } | SessionMiss;

function rehydrate(): Promise<Rehydrated> {
  if (rehydratePromise) return rehydratePromise;
  rehydratePromise = (async (): Promise<Rehydrated> => {
    try {
      const check = await refreshSession();
      if (check.kind !== "signed-in") return check;
      return { kind: "signed-in", user: await currentUser() };
    } catch (err) {
      // WhoAmI failed: only a refusal is no session, an outage is not.
      return classifySessionError(err);
    } finally {
      // Cleared after a tick so the double mount joins this promise but a
      // later re-mount (after logout) asks again.
      setTimeout(() => {
        rehydratePromise = null;
      }, 0);
    }
  })();
  return rehydratePromise;
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null);
  const [status, setStatus] = useState<AuthStatus>("loading");
  const [error, setError] = useState<string | null>(null);

  // Bumped by every sign-in, tenant switch and sign-out. The on-mount
  // rehydrate applies its answer only if none of them happened while it was
  // in flight: a slow "no session" arriving after a login succeeded would
  // otherwise sign the user straight back out to /login.
  const generation = useRef(0);

  useEffect(() => {
    let cancelled = false;
    let retry: ReturnType<typeof setTimeout> | undefined;
    const startedAt = generation.current;
    // A sign-in, switch or sign-out since the first check supersedes it, and
    // ends its retries.
    const superseded = () => cancelled || generation.current !== startedAt;
    const check = (attempt: number) => {
      if (superseded()) return;
      void rehydrate().then((found) => {
        if (superseded()) return;
        switch (found.kind) {
          case "signed-in":
            setUser(found.user);
            setStatus("authenticated");
            return;
          case "signed-out":
            setStatus("unauthenticated");
            return;
          case "unavailable":
            setStatus("unavailable");
            retry = setTimeout(
              () => check(attempt + 1),
              sessionRetryDelayMs(attempt, found.retryAfterMs),
            );
        }
      });
    };
    check(0);
    return () => {
      cancelled = true;
      clearTimeout(retry);
    };
  }, []);

  // A call the role answered Unauthenticated: the session ended elsewhere.
  useEffect(
    () =>
      onSessionEnded(() => {
        generation.current++;
        setUser(null);
        setStatus("unauthenticated");
      }),
    [],
  );

  const login = useCallback(async (subject: string, password: string) => {
    generation.current++;
    setError(null);
    setStatus("loading");
    try {
      await signIn(subject, password);
      setUser(await currentUser());
      setStatus("authenticated");
    } catch (e) {
      setError(loginFailure(e));
      setStatus("unauthenticated");
      throw e;
    }
  }, []);

  const switchTenant = useCallback(async (tenantId: string) => {
    generation.current++;
    await switchSessionTenant(tenantId);
    setUser(await currentUser());
    setStatus("authenticated");
  }, []);

  const logout = useCallback(async () => {
    generation.current++;
    try {
      await signOut();
    } finally {
      setUser(null);
      setStatus("unauthenticated");
    }
  }, []);

  const value = useMemo<AuthContextValue>(
    () => ({ user, status, error, login, switchTenant, logout }),
    [user, status, error, login, switchTenant, logout],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) {
    throw new Error("useAuth must be used within <AuthProvider>");
  }
  return ctx;
}
