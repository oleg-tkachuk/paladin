import { NextResponse } from "next/server";

import { AUDIENCES } from "@/constants";
import {
  iamAuthClient,
  readSessionCookie,
  clearSessionCookie,
} from "@/lib/auth/bff";

/**
 * POST /api/auth/logout
 *
 * Revokes the active refresh chain server-side, then clears the session
 * cookie. The cookie is cleared even if revocation fails, because a user who
 * clicked "log out" must end up logged out of THIS browser regardless — but
 * the two are not equivalent, and the response says which happened.
 *
 * Revoke needs an Authorization header. The IAM plane's permissive
 * interceptor waives one for Login, RefreshToken and ExchangeAudience only,
 * and this route sent none — so every logout failed with "missing
 * Authorization header", the error was swallowed by a catch, the cookie was
 * cleared, and the user appeared logged out while the refresh chain stayed
 * live for its full seven days. Anyone still holding that cookie value kept a
 * working session.
 *
 * The bearer is derived with ExchangeAudience, which consumes nothing: this
 * route is about to revoke the chain, so rotating it first would be both
 * wasteful and a way to leave a fresh token behind if the revoke then failed.
 *
 * The alternative — adding Revoke to the permissive list — is defensible on
 * RFC 7009 grounds, since possession of the refresh token in the body is
 * already the proof of ownership and the handler never reads the caller's
 * principal. It is not taken here because it widens the unauthenticated
 * surface to fix a caller that can simply authenticate.
 */

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export async function POST(): Promise<NextResponse> {
  const token = await readSessionCookie();
  let revoked = false;
  if (token) {
    try {
      const { accessToken } = await iamAuthClient().exchangeAudience({
        refreshToken: token,
        targetAudience: AUDIENCES.iam,
      });
      await iamAuthClient().revoke(
        { token },
        { headers: { Authorization: `Bearer ${accessToken}` } },
      );
      revoked = true;
    } catch (err) {
      // Error, not warn. A logout that did not revoke leaves a live session
      // behind the user's back; that is the kind of thing an operator needs
      // to find in the log, not a line they have learned to scroll past.
      console.error("[BFF /logout] server-side revoke FAILED:", err);
    }
  }
  const out = NextResponse.json({ ok: true, revoked });
  clearSessionCookie(out);
  return out;
}
