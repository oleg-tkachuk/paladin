import { NextResponse, type NextRequest } from "next/server";

import { AUDIENCES } from "@/constants";
import { refreshCookieName } from "@/lib/auth/cookies";

/**
 * Edge middleware — first line of route protection.
 *
 * MUST stay named `middleware.ts` with `export function middleware`. The
 * Next.js 16 `proxy.ts` / `export function proxy` convention is detected
 * and compiled (build prints "ƒ Proxy (Middleware)") but as of 16.2.6 it
 * is NOT written into `middleware-manifest.json`, so the standalone
 * runtime never executes it — the gate silently does nothing. The
 * `middleware` convention populates the manifest correctly; the
 * deprecation warning it prints is cosmetic. Re-test the manifest before
 * switching to `proxy` on any future Next upgrade.
 *
 * Treats the presence of the iam refresh-token cookie as a coarse "logged-in"
 * signal: if it's missing, redirect to /login with the original path as
 * `?next=`. This is a UX gate (skip rendering the shell for an obvious-anon
 * visitor), NOT the security boundary — the real identity/authz checks are
 * the backend JWT + Cedar interceptors, with the BFF validating audience
 * before forwarding (see app/api/rpc/[[...connect]]/route.ts).
 *
 * Excluded:
 *   - /login itself
 *   - /api/auth/* (login/logout/refresh/exchange/me — must stay reachable
 *     while logged-out)
 *   - Static assets (_next, favicon, public files)
 */

const IAM_REFRESH_COOKIE = refreshCookieName(AUDIENCES.iam);

const PUBLIC_PREFIXES = [
  "/login",
  "/api/auth",
  "/_next",
  "/favicon.ico",
  "/robots.txt",
];

export function middleware(req: NextRequest) {
  const { pathname } = req.nextUrl;

  if (
    PUBLIC_PREFIXES.some((p) => pathname === p || pathname.startsWith(p + "/"))
  ) {
    return NextResponse.next();
  }

  const hasSession = req.cookies.get(IAM_REFRESH_COOKIE)?.value;
  if (hasSession) return NextResponse.next();

  const loginUrl = req.nextUrl.clone();
  loginUrl.pathname = "/login";
  loginUrl.search = `?next=${encodeURIComponent(pathname + req.nextUrl.search)}`;
  return NextResponse.redirect(loginUrl);
}

export const config = {
  // Match everything except Next internals + static files. The function
  // itself filters PUBLIC_PREFIXES — keeping the matcher loose lets us add
  // routes to that list without touching this regex.
  matcher: ["/((?!_next/static|_next/image|favicon.ico).*)"],
};
