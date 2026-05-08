import { createClient, type Client } from "@connectrpc/connect";
import { createGrpcWebTransport } from "@connectrpc/connect-web";
import { cookies } from "next/headers";
import type { NextResponse } from "next/server";

import { AUDIENCES, type Audience } from "@/constants";
import { AuthService } from "@/gen/paladin/iam/v1/auth_service_pb";
import { refreshCookieName } from "./cookies";

export { refreshCookieName };

/**
 * Server-side helpers for the BFF auth routes (/api/auth/*).
 *
 * The BFF holds exactly ONE refresh-token chain — bound to the iam
 * audience — and uses AuthService.ExchangeAudience to derive short-lived
 * access tokens for the data and admin planes on demand. The single
 * cookie is `paladin_rt_iam`. The Helm chart's bootstrap admin doesn't care
 * which audience the chain starts on; iam is just the conventional home.
 *
 * One cookie keeps the BFF state minimal — the data/admin chains the v1
 * design carried as separate cookies are gone.
 */

const IAM_BACKEND_URL =
  process.env.PALADIN_IAM_URL || "http://paladin:8085";

let iamClientCache: Client<typeof AuthService> | null = null;

export function iamAuthClient(): Client<typeof AuthService> {
  if (iamClientCache) return iamClientCache;
  const transport = createGrpcWebTransport({ baseUrl: IAM_BACKEND_URL });
  iamClientCache = createClient(AuthService, transport);
  return iamClientCache;
}

// ────────────────────────── Cookie helpers ──────────────────────────

// Path "/" so the proxy middleware (proxy.ts) sees the cookie on every
// route, not just /api/auth/*. The earlier narrow scope of /api/auth
// looked tighter on paper but in practice meant the middleware had no
// signal to distinguish "logged in" from "anonymous" on /, /buckets,
// etc., so it redirected logged-in users straight back to /login —
// classic post-login redirect loop. The cookie is httpOnly + SameSite=
// Strict so widening the path doesn't add JS-access surface.
const COOKIE_PATH = "/";

/** The single refresh-token cookie — pinned to the iam audience. */
export const SESSION_COOKIE_NAME = refreshCookieName(AUDIENCES.iam);

type CookieOpts = {
  maxAgeSeconds: number;
};

const baseCookieAttrs = (maxAge: number) => ({
  httpOnly: true,
  sameSite: "strict" as const,
  secure: process.env.NODE_ENV === "production",
  path: COOKIE_PATH,
  maxAge,
});

/**
 * Attach the session cookie to a NextResponse.
 *
 * The earlier `cookies().set()` form (next/headers) is fine for the
 * very first response on a request (Next.js merges its mutations into
 * the auto-built response), but it stops propagating reliably when the
 * handler builds a NextResponse explicitly via NextResponse.json() —
 * the response's own cookie jar is the only thing the framework
 * serialises, so the merged jar gets dropped silently. Symptom: rotated
 * cookies in /api/auth/exchange (audience=paladin-iam) and /api/auth/me
 * never reached the browser, leading to "no session cookie" on the
 * very next call. Writing through `res.cookies.set()` is the
 * unambiguous form.
 */
export function setSessionCookie(
  res: NextResponse,
  refreshToken: string,
  opts: CookieOpts,
) {
  res.cookies.set(
    SESSION_COOKIE_NAME,
    refreshToken,
    baseCookieAttrs(opts.maxAgeSeconds),
  );
}

export async function readSessionCookie(): Promise<string | null> {
  const jar = await cookies();
  return jar.get(SESSION_COOKIE_NAME)?.value ?? null;
}

export function clearSessionCookie(res: NextResponse) {
  res.cookies.set(SESSION_COOKIE_NAME, "", baseCookieAttrs(0));
}

// ────────────────────────── RefreshToken dedup ──────────────────────────

/**
 * AuthService.RefreshToken rotates the refresh chain — the supplied
 * token is consumed and a new one is issued. That makes concurrent
 * calls with the same input token catastrophic: the second one sees
 * a "refresh token rejected" because its parent has already been
 * replaced. In the BFF this happens on every page load — /api/auth/me
 * fires from AuthProvider, and the iamTransport interceptor often
 * fires /api/auth/exchange (audience=paladin-iam) at the same instant
 * for an early RPC. Both call RefreshToken; whichever loses the race
 * sees a 401 → AuthContext flips to unauthenticated → tenant badge
 * shows "no tenant" even though the session is valid.
 *
 * Dedup keyed on the incoming refresh-token value: while one
 * rotation is in flight, sibling callers join the same Promise and
 * receive the same new pair. The map self-cleans on settle so a
 * crashed Next.js process doesn't leak memory between requests.
 *
 * Single-instance assumption — fine for dev and small deployments.
 * Multi-replica BFF would need a shared cache (Redis), but that's
 * out of scope until the deployment topology actually changes.
 */

type RotateResult = {
  accessToken: string;
  refreshToken: string;
  accessExpiresInSeconds: number;
  refreshExpiresInSeconds: number;
};

const inflightRotations = new Map<string, Promise<RotateResult>>();

export async function refreshIamChain(
  refreshToken: string,
  audience: Audience,
): Promise<RotateResult> {
  const key = `${audience}::${refreshToken}`;
  const existing = inflightRotations.get(key);
  if (existing) return existing;

  const promise = (async () => {
    const res = await iamAuthClient().refreshToken({
      refreshToken,
      requestedAudience: audience,
    });
    if (!res.tokens) {
      throw new Error("IAM RefreshToken returned no token pair");
    }
    return {
      accessToken: res.tokens.accessToken,
      refreshToken: res.tokens.refreshToken,
      accessExpiresInSeconds: Number(res.tokens.accessExpiresInSeconds),
      refreshExpiresInSeconds: Number(res.tokens.refreshExpiresInSeconds),
    };
  })().finally(() => {
    inflightRotations.delete(key);
  });

  inflightRotations.set(key, promise);
  return promise;
}

// ────────────────────────── DTO shaping ──────────────────────────

/**
 * Browser-facing user payload. Strips proto-runtime fields ($typeName,
 * Timestamp objects) so the AuthContext can JSON.stringify it freely.
 */
export type UserDTO = {
  userId: string;
  tenantId: string;
  subject: string;
  displayName: string;
  roles: string[];
  resourceVersion: string;
};

export function toUserDTO(u: {
  userId: string;
  tenantId: string;
  subject: string;
  displayName: string;
  roles: string[];
  resourceVersion: string;
}): UserDTO {
  return {
    userId: u.userId,
    tenantId: u.tenantId,
    subject: u.subject,
    displayName: u.displayName,
    roles: u.roles,
    resourceVersion: u.resourceVersion,
  };
}

export type AccessTokenDTO = {
  audience: Audience;
  token: string;
  // Epoch ms when the token expires; the client stores it in tokenStore so
  // refresh fires ~30s ahead of the deadline.
  expiresAt: number;
};

export function toAccessTokenDTO(
  audience: Audience,
  accessToken: string,
  accessExpiresInSeconds: number | bigint,
): AccessTokenDTO {
  const ttl = Number(accessExpiresInSeconds);
  return {
    audience,
    token: accessToken,
    expiresAt: Date.now() + ttl * 1000,
  };
}
