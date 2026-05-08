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

/**
 * GET /api/auth/me
 *
 * Re-hydration endpoint for client-side AuthContext on page load. Reads
 * the session cookie, refreshes the iam chain to get a short-lived iam
 * access token, calls WhoAmI, returns { user, accessTokens } with the iam
 * audience pre-seeded. Tokens for data/admin lazy-fetch via /exchange on
 * the first RPC into those planes.
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
    // Use refreshIamChain (NOT a raw RefreshToken call) so concurrent
    // /api/auth/me + /api/auth/exchange callers share the same in-flight
    // rotation. Without dedup, the second-arriving caller sees "refresh
    // token rejected" because its parent token has already been
    // consumed — which surfaces in the UI as a phantom logout.
    const tokens = await refreshIamChain(refreshToken, iamAudience);

    const whoAmI = await iamAuthClient().whoAmI(
      {},
      { headers: { Authorization: `Bearer ${tokens.accessToken}` } },
    );
    if (!whoAmI.user) {
      return NextResponse.json(
        { error: "WhoAmI returned no user" },
        { status: 502 },
      );
    }

    const payload: MeResponseBody = {
      user: toUserDTO(whoAmI.user),
      accessTokens: [
        toAccessTokenDTO(
          iamAudience,
          tokens.accessToken,
          tokens.accessExpiresInSeconds,
        ),
      ],
    };
    const out = NextResponse.json(payload);
    setSessionCookie(out, tokens.refreshToken, {
      maxAgeSeconds: tokens.refreshExpiresInSeconds,
    });
    return out;
  } catch (err) {
    console.error("[BFF /me]", err);
    return NextResponse.json(
      { error: (err as Error).message || "auth check failed" },
      { status: 401 },
    );
  }
}
