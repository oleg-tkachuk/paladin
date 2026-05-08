import { NextResponse, type NextRequest } from "next/server";

import { AUDIENCES } from "@/constants";
import { refreshCookieName } from "@/lib/auth/cookies";

/**
 * Edge proxy — first line of route protection. (Was `middleware.ts` before
 * Next.js 16; the convention renamed to `proxy.ts` + `export function proxy`.)
 *
 * Treats the presence of the iam refresh-token cookie as a coarse "logged-in"
 * signal: if it's missing, redirect to /login with the original path as
 * `?next=`. The actual identity check still happens at the BFF and via
 * AuthContext on the client; this is just to skip rendering the shell for
 * an obvious-anon visitor.
 *
 * Excluded:
 *   - /login itself
 *   - /api/auth/* (login/logout/refresh/exchange/me — must stay reachable
 *     while logged-out)
 *   - Static assets (_next, favicon, public files)
 *
 * Note: the rest of /api/* (e.g. /api/rpc) IS gated. The transport layer
 * adds Authorization headers from tokenStore; if the cookie is missing,
 * tokenStore can't fetch tokens anyway and the BFF will 401.
 */

const IAM_REFRESH_COOKIE = refreshCookieName(AUDIENCES.iam);

const PUBLIC_PREFIXES = [
  "/login",
  "/api/auth",
  "/_next",
  "/favicon.ico",
  "/robots.txt",
];

export function proxy(req: NextRequest) {
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
