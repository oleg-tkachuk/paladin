"use client";

// AuthGate — global authentication wall.
//
// Every route except the STANDALONE_ROUTES allowlist sits behind this
// component. While AuthContext rehydrates (status="loading") we render
// a thin Skeleton splash. On status="unauthenticated" we redirect to
// /login, preserving the originally-requested URL in a `?next=…`
// query param so the login flow can bounce the user back to the page
// they intended to visit. On "authenticated" we render the children.
//
// This is the security contract for the UI:
//   - operator typing https://paladin.local/tenants without a session
//     never sees the page chrome OR data
//   - the existing AuthContext.rehydrate() path is the only way to
//     reach an authenticated state (a real /api/auth/me round-trip)
//   - no "loading skeleton" path leaks tenant data into the DOM
//
// Pages do not need their own per-route auth checks — the gate covers
// every authed surface. The RPCs themselves are also gated server-side
// (Cedar + JWT verification), so a stale frontend can't accidentally
// fetch data on its way back to /login.

import React, { useEffect } from "react";
import { useRouter, usePathname } from "next/navigation";

import { useAuth } from "@/context/AuthContext";
import { Skeleton } from "@/components/ui/Skeleton";

interface AuthGateProps {
  /** Pathname patterns that bypass the gate (e.g. /login). */
  publicRoutes: Set<string>;
  children: React.ReactNode;
}

export function AuthGate({ publicRoutes, children }: AuthGateProps) {
  const { status } = useAuth();
  const router = useRouter();
  const pathname = usePathname();

  const isPublic = publicRoutes.has(pathname);

  useEffect(() => {
    if (isPublic) return;
    if (status !== "unauthenticated") return;
    // Preserve the originally-requested URL so the login form can
    // bounce back. /login itself ignores `?next=/login` to avoid
    // loops; we don't bother stripping it here.
    const next =
      typeof window !== "undefined" && window.location
        ? window.location.pathname + window.location.search
        : pathname;
    const qs =
      next && next !== "/login" ? `?next=${encodeURIComponent(next)}` : "";
    router.replace(`/login${qs}`);
  }, [status, isPublic, pathname, router]);

  // Public routes render immediately; the AuthProvider still mounts
  // upstream and continues rehydrating in the background.
  if (isPublic) return <>{children}</>;

  // While the rehydrate /api/auth/me round-trip is in flight, show a
  // minimal splash. We deliberately do NOT render the children behind
  // a blur or overlay — a flash of the dashboard before the redirect
  // would leak tenant data through the DOM, even briefly.
  if (status === "loading") {
    return (
      <div className="flex min-h-screen items-center justify-center bg-background">
        <div className="w-72 space-y-3">
          <Skeleton className="mx-auto h-8 w-32" />
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-5/6" />
          <Skeleton className="h-4 w-4/6" />
        </div>
      </div>
    );
  }

  // The useEffect above kicked off the router.replace; while React
  // navigates we return nothing. This is the half-second between
  // status flipping to "unauthenticated" and the URL changing.
  if (status === "unauthenticated") {
    return null;
  }

  return <>{children}</>;
}
