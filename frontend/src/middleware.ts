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
  // Health probes (kubelet liveness/readiness) are unauthenticated and
  // must NOT be redirected to /login — otherwise the probe gets a 307
  // (which kubelet treats as success) and the real liveness route never
  // runs, defeating the probe.
  "/api/health",
  // OAuth consent (ADR-0009) is a pre-login flow: the user arrives here
  // unauthenticated to grant a client access, so it must not bounce to /login.
  "/oauth",
  "/_next",
  "/favicon.ico",
  "/robots.txt",
];

// Methods that mutate state and therefore need CSRF protection. Safe
// methods (GET/HEAD/OPTIONS) are exempt.
const UNSAFE_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"]);

export function middleware(req: NextRequest) {
  const { pathname } = req.nextUrl;

  // CSRF defense: a state-changing request to any API route must come
  // from our own origin. The session cookie is SameSite=Strict, but
  // that alone doesn't cover related-subdomain or older-browser vectors
  // on a control plane — so we also require the Origin header (sent by
  // browsers on all cross-origin and same-origin POSTs) to match the
  // request host. Non-browser callers (no Origin) are allowed through;
  // they don't carry the ambient cookie a CSRF attack relies on.
  if (pathname.startsWith("/api/") && UNSAFE_METHODS.has(req.method)) {
    const origin = req.headers.get("origin");
    if (origin) {
      let originHost = "";
      try {
        originHost = new URL(origin).host;
      } catch {
        originHost = "";
      }
      // Compare against the PUBLIC host the browser used, not
      // req.nextUrl.host — behind Traefik the latter is the in-cluster
      // address (paladin-console:3000) while the browser's
      // Origin carries the ingress host (paladin.example.com), so a naive
      // compare 403s every legitimate browser POST. X-Forwarded-Host is
      // set by our trusted ingress; Host covers direct/dev access. Origin
      // itself is browser-enforced (a cross-site attacker can't forge
      // it), so matching either of our own hostnames is a sound CSRF gate.
      const acceptableHosts = [
        req.headers.get("x-forwarded-host"),
        req.headers.get("host"),
        req.nextUrl.host,
      ].filter((h): h is string => !!h);
      if (!originHost || !acceptableHosts.includes(originHost)) {
        return NextResponse.json(
          {
            code: "permission_denied",
            message: "cross-origin request rejected",
          },
          { status: 403 },
        );
      }
    }
  }

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
