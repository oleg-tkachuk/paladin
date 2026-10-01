import { Code, ConnectError } from "@connectrpc/connect";
import { NextResponse } from "next/server";

import { AUDIENCES, type Audience } from "@/constants";
import {
  iamAuthClient,
  readSessionCookie,
  refreshIamChain,
  setSessionCookie,
  toUserDTO,
  toAccessTokenDTO,
  type AccessTokenDTO,
  type UserDTO,
} from "@/lib/auth/bff";
import { rotationDue } from "@/lib/auth/rotation";

/**
 * GET /api/auth/me
 *
 * Re-hydration endpoint for client-side AuthContext on page load. Reads
 * the session cookie, derives a short-lived iam access token — rotating the
 * chain only once the refresh token is due (lib/auth/rotation.ts) — calls
 * WhoAmI, and returns { user, accessTokens } with the iam audience pre-seeded. Tokens for data/admin lazy-fetch via /exchange on
 * the first RPC into those planes.
 *
 * This is the ONLY route that rotates the chain, and two of them can still
 * run at once: two tabs opened together each bootstrap a session, and the
 * browser cannot update the shared cookie between two requests already in
 * flight. The server decides that race in the database and tells the loser so
 * (Aborted). The loser then does the only thing it actually needed — derive an
 * iam access token — and returns without writing a cookie, because the winner
 * is writing the real one. See mintIamAccess below.
 */

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

type MeResponseBody = {
  user: UserDTO;
  accessTokens: AccessTokenDTO[];
};

export async function GET(): Promise<NextResponse> {
  const iamAudience: Audience = AUDIENCES.iam;
  const refreshToken = await readSessionCookie();
  if (!refreshToken) {
    return NextResponse.json({ error: "not authenticated" }, { status: 401 });
  }

  try {
    const minted = rotationDue(refreshToken, Date.now())
      ? await mintIamAccess(refreshToken, iamAudience)
      : await exchangeIamAccess(refreshToken, iamAudience);

    const whoAmI = await iamAuthClient().whoAmI(
      {},
      { headers: { Authorization: `Bearer ${minted.accessToken}` } },
    );
    if (!whoAmI.user) {
      return NextResponse.json(
        { error: "WhoAmI returned no user" },
        { status: 502 },
      );
    }

    const payload: MeResponseBody = {
      // tenantSlug comes from WhoAmIResponse.tenant_slug — populated
      // server-side from the JWT principal claim. Empty string when
      // the token was minted before the slug-claim wiring (legacy
      // sessions); the SPA falls back to tenantId for URL routing.
      user: toUserDTO(whoAmI.user, whoAmI.tenantSlug),
      accessTokens: [
        toAccessTokenDTO(
          iamAudience,
          minted.accessToken,
          minted.accessExpiresInSeconds,
        ),
      ],
    };
    const out = NextResponse.json(payload);
    if (minted.rotated) {
      setSessionCookie(out, minted.rotated.refreshToken, {
        maxAgeSeconds: minted.rotated.refreshExpiresInSeconds,
      });
    }
    return out;
  } catch (err) {
    console.error("[BFF /me]", err);
    return NextResponse.json(
      { error: (err as Error).message || "auth check failed" },
      { status: 401 },
    );
  }
}

type MintedIam = {
  accessToken: string;
  accessExpiresInSeconds: number;
  // Present only when THIS request performed the rotation. The loser of a
  // race must not write a cookie: it has no successor to write, and writing
  // the token it already holds would undo the winner's Set-Cookie depending
  // on which response the browser applied last.
  rotated?: { refreshToken: string; refreshExpiresInSeconds: number };
};

/**
 * Rotate the chain and return the resulting iam access token — or, if a
 * sibling request rotated first, derive one without rotating.
 *
 * RefreshToken answers Aborted when its conditional supersession matched no
 * row, which is the database saying "another request traded this token in
 * while you were reading it". That is not a stolen token and the server does
 * not treat it as one: the family survives and reuse detection is untouched,
 * because the loser is handed no refresh token at all. It only ever needed an
 * access token, and ExchangeAudience mints one from a just-superseded token
 * within the same grace window, consuming nothing.
 *
 * Any other error propagates. An expired or genuinely revoked token must
 * still fail this route.
 */
async function mintIamAccess(
  refreshToken: string,
  audience: Audience,
): Promise<MintedIam> {
  try {
    const t = await refreshIamChain(refreshToken, audience);
    return {
      accessToken: t.accessToken,
      accessExpiresInSeconds: t.accessExpiresInSeconds,
      rotated: {
        refreshToken: t.refreshToken,
        refreshExpiresInSeconds: t.refreshExpiresInSeconds,
      },
    };
  } catch (err) {
    if (!(err instanceof ConnectError) || err.code !== Code.Aborted) throw err;
    return exchangeIamAccess(refreshToken, audience);
  }
}

/**
 * An iam access token from the refresh token, leaving the chain untouched: no
 * refresh token comes back, so no cookie is written and nothing can be lost
 * with the response.
 */
async function exchangeIamAccess(
  refreshToken: string,
  audience: Audience,
): Promise<MintedIam> {
  const res = await iamAuthClient().exchangeAudience({
    refreshToken,
    targetAudience: audience,
  });
  return {
    accessToken: res.accessToken,
    accessExpiresInSeconds: Number(res.accessExpiresInSeconds),
  };
}
