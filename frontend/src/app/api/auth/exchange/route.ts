import { NextResponse } from "next/server";

import { AUDIENCES, type Audience } from "@/constants";
import {
  iamAuthClient,
  readSessionCookie,
  refreshIamChain,
  setSessionCookie,
  toAccessTokenDTO,
  type AccessTokenDTO,
} from "@/lib/auth/bff";

/**
 * POST /api/auth/exchange
 * Body: { audience }
 *
 * Returns a fresh access token for the requested audience. Reads the iam
 * refresh-token cookie and either:
 *
 *   - audience=paladin-iam: calls RefreshToken (rotates the chain — the new
 *     refresh token replaces the old one in the cookie). This is the
 *     normal "iam access expired" path.
 *   - audience=paladin-data | paladin-admin: calls ExchangeAudience, which
 *     mints a short-lived access token without rotating the iam refresh
 *     chain. Cookie is left untouched.
 */

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

const VALID_AUDIENCES = new Set<Audience>(
  Object.values(AUDIENCES) as Audience[],
);

export async function POST(req: Request): Promise<NextResponse> {
  let body: { audience?: string };
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON" }, { status: 400 });
  }

  const audience = body.audience as Audience | undefined;
  if (!audience || !VALID_AUDIENCES.has(audience)) {
    return NextResponse.json(
      { error: `audience must be one of ${[...VALID_AUDIENCES].join(", ")}` },
      { status: 400 },
    );
  }

  const refreshToken = await readSessionCookie();
  if (!refreshToken) {
    return NextResponse.json(
      { error: "no session cookie — login required" },
      { status: 401 },
    );
  }

  try {
    if (audience === AUDIENCES.iam) {
      // iam audience uses RefreshToken (rotates the chain). Routed
      // through refreshIamChain so this call dedupes against any
      // concurrent /api/auth/me — both share the same in-flight
      // rotation and receive the same new pair, instead of fighting
      // for a single-use refresh token.
      const tokens = await refreshIamChain(refreshToken, AUDIENCES.iam);
      const payload: AccessTokenDTO = toAccessTokenDTO(
        audience,
        tokens.accessToken,
        tokens.accessExpiresInSeconds,
      );
      const out = NextResponse.json(payload);
      setSessionCookie(out, tokens.refreshToken, {
        maxAgeSeconds: tokens.refreshExpiresInSeconds,
      });
      return out;
    }

    // data / admin: ExchangeAudience derives an access token without
    // touching the refresh chain.
    const res = await iamAuthClient().exchangeAudience({
      refreshToken,
      targetAudience: audience,
    });
    const payload: AccessTokenDTO = toAccessTokenDTO(
      audience,
      res.accessToken,
      res.accessExpiresInSeconds,
    );
    return NextResponse.json(payload);
  } catch (err) {
    console.error(`[BFF /exchange] audience=${audience}`, err);
    return NextResponse.json(
      { error: (err as Error).message || "exchange failed" },
      { status: 401 },
    );
  }
}
