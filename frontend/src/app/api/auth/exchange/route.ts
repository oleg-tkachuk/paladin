import { NextResponse } from "next/server";

import { AUDIENCES, type Audience } from "@/constants";
import {
  iamAuthClient,
  readSessionCookie,
  toAccessTokenDTO,
  type AccessTokenDTO,
} from "@/lib/auth/bff";

/**
 * POST /api/auth/exchange
 * Body: { audience }
 *
 * Returns a fresh access token for the requested audience. Reads the iam
 * refresh-token cookie and calls ExchangeAudience, which mints a short-lived
 * access token without consuming the chain — for every audience, iam
 * included. The cookie is never written here.
 *
 * The iam case used to call RefreshToken instead, on the reasoning that an
 * expired iam access token is what a refresh is for. But that made a single
 * page load rotate the chain twice — once here and once in /api/auth/me —
 * with the browser unable to update the cookie in between, so whichever
 * request lost read a consumed token. Deriving instead of rotating removes
 * the second rotation rather than coordinating it.
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
    // Every audience, iam included, goes through ExchangeAudience. The iam
    // case used to call RefreshToken instead, which ROTATES the chain — so a
    // page load fired two rotations of one cookie: this route and
    // /api/auth/me. That was the race the BFF then spent an in-process index
    // and a dedup map collapsing. ExchangeAudience mints an iam access token
    // perfectly well (assertAudienceAllowed permits it outright) and consumes
    // nothing, so there is no second rotation left to collapse.
    //
    // The session still slides: /api/auth/me rotates once per page load,
    // which is what advances the chain. This route deriving a token no longer
    // does, and no longer needs to.
    // Sent as-is. The token may already have been rotated by a sibling request
    // — a browser cannot update its cookie between two requests in flight —
    // and the server tolerates that itself now: a refresh token superseded
    // within its grace window is honoured by ExchangeAudience, which consumes
    // nothing and so gives up no rotation guarantee.
    //
    // This used to be a walk through an in-process index of rotations, plus a
    // retry that re-read the cookie. Both are gone, and with them the reason
    // this process could not run in more than one replica: a request routed to
    // the replica that did not perform the rotation had nothing to follow, and
    // the operator was signed out by a race they could not see. The knowledge
    // belongs where the tokens are minted, not in a copy kept by every client.
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
