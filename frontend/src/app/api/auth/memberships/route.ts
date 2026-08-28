import { NextResponse } from "next/server";

import { AUDIENCES, type Audience } from "@/constants";
import { iamAuthClient, readSessionCookie } from "@/lib/auth/bff";

/**
 * GET /api/auth/memberships
 *
 * Lists every tenant the signed-in subject belongs to (AuthService.
 * ListMyMemberships), for the ScopePicker's tenant switcher. Reads the
 * session cookie, derives a short-lived iam access token from it without
 * consuming it (ExchangeAudience), and calls the RPC. The cookie is left
 * exactly as it was: only /api/auth/me rotates the chain.
 */

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

type MembershipDTO = {
  tenantId: string;
  tenantSlug: string;
  roles: string[];
  disabled: boolean;
  current: boolean;
};

export async function GET(): Promise<NextResponse> {
  const iamAudience: Audience = AUDIENCES.iam;
  const refreshToken = await readSessionCookie();
  if (!refreshToken) {
    return NextResponse.json({ error: "not authenticated" }, { status: 401 });
  }

  try {
    // ExchangeAudience, not RefreshToken: this needs an iam access token to
    // make one read-only call, and rotating the chain to get one made every
    // tenant-switcher render a second rotation racing whatever else the page
    // was doing with the same cookie.
    const { accessToken } = await iamAuthClient().exchangeAudience({
      refreshToken,
      targetAudience: iamAudience,
    });
    const res = await iamAuthClient().listMyMemberships(
      {},
      { headers: { Authorization: `Bearer ${accessToken}` } },
    );
    const memberships: MembershipDTO[] = res.memberships.map((m) => ({
      tenantId: m.tenantId,
      tenantSlug: m.tenantSlug,
      roles: m.roles,
      disabled: m.disabled,
      current: m.current,
    }));
    // No Set-Cookie: nothing here consumed the refresh token, so re-writing
    // the cookie would be asserting a rotation that did not happen.
    return NextResponse.json({ memberships });
  } catch (err) {
    console.error("[BFF /memberships]", err);
    return NextResponse.json(
      { error: (err as Error).message || "failed to list memberships" },
      { status: 401 },
    );
  }
}
