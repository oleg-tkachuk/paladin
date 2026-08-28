import { NextResponse } from "next/server";

import { AUDIENCES, type Audience } from "@/constants";
import {
  iamAuthClient,
  readSessionCookie,
  setSessionCookie,
  toUserDTO,
  toAccessTokenDTO,
  type AccessTokenDTO,
  type UserDTO,
} from "@/lib/auth/bff";

/**
 * POST /api/auth/switch-tenant
 * Body: { targetTenantId }
 *
 * Switches the session to a DIFFERENT tenant the signed-in subject is a
 * member of. Reads the current session cookie, refreshes the iam chain for
 * a short-lived access token to authenticate the call, invokes
 * AuthService.SwitchTenant (which mints a fresh iam pair scoped to the
 * target — a new refresh family), then replaces the session cookie with the
 * target's refresh token. A follow-up WhoAmI resolves the target tenant slug
 * so the returned user record is complete (mirrors /api/auth/me).
 *
 * The response shape matches /login + /me so AuthContext can reseed the
 * token cache and user identically.
 */

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

type SwitchRequestBody = { targetTenantId?: string };
type SwitchResponseBody = { user: UserDTO; accessTokens: AccessTokenDTO[] };

export async function POST(req: Request): Promise<NextResponse> {
  let body: SwitchRequestBody;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON" }, { status: 400 });
  }
  const targetTenantId = body.targetTenantId?.trim() ?? "";
  if (!targetTenantId) {
    return NextResponse.json(
      { error: "targetTenantId is required" },
      { status: 400 },
    );
  }

  const iamAudience: Audience = AUDIENCES.iam;
  const refreshToken = await readSessionCookie();
  if (!refreshToken) {
    return NextResponse.json({ error: "not authenticated" }, { status: 401 });
  }

  try {
    // Authenticate the switch with an iam access token DERIVED from the
    // current session rather than a rotation of it. SwitchTenant issues its
    // own token pair below, which replaces the cookie outright, so rotating
    // first only consumed a link in a chain that was about to be discarded —
    // while racing whatever else the page held the same cookie open for.
    const { accessToken } = await iamAuthClient().exchangeAudience({
      refreshToken,
      targetAudience: iamAudience,
    });
    const sw = await iamAuthClient().switchTenant(
      { targetTenantId, requestedAudience: iamAudience },
      { headers: { Authorization: `Bearer ${accessToken}` } },
    );
    const tokens = sw.tokens;
    if (!tokens) {
      return NextResponse.json(
        { error: "SwitchTenant returned no token pair" },
        { status: 502 },
      );
    }

    // Resolve the target tenant slug (SwitchTenantResponse.user carries the
    // tenant id but not the slug) so URL routing works post-switch.
    const whoAmI = await iamAuthClient().whoAmI(
      {},
      { headers: { Authorization: `Bearer ${tokens.accessToken}` } },
    );
    const user = whoAmI.user ?? sw.user;
    if (!user) {
      return NextResponse.json(
        { error: "SwitchTenant returned no user" },
        { status: 502 },
      );
    }

    const payload: SwitchResponseBody = {
      user: toUserDTO(user, whoAmI.tenantSlug),
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
      maxAgeSeconds: Number(tokens.refreshExpiresInSeconds),
    });
    return out;
  } catch (err) {
    console.error("[BFF /switch-tenant]", err);
    // PermissionDenied (not a member / disabled) surfaces as 403; anything
    // else as 401 so the client re-checks the session.
    const msg = (err as Error).message || "switch failed";
    const status = /permission|not a member|disabled/i.test(msg) ? 403 : 401;
    return NextResponse.json({ error: msg }, { status });
  }
}
