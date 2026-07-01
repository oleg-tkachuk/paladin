import { NextResponse } from "next/server";

import { AUDIENCES, type Audience } from "@/constants";
import {
  iamAuthClient,
  readSessionCookie,
  refreshIamChain,
  setSessionCookie,
} from "@/lib/auth/bff";

/**
 * GET /api/auth/memberships
 *
 * Lists every tenant the signed-in subject belongs to (AuthService.
 * ListMyMemberships), for the ScopePicker's tenant switcher. Reads the
 * session cookie, refreshes the iam chain for a short-lived access token,
 * calls the RPC with it, and rotates the cookie (like /api/auth/me).
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
    const tokens = await refreshIamChain(refreshToken, iamAudience);
    const res = await iamAuthClient().listMyMemberships(
      {},
      { headers: { Authorization: `Bearer ${tokens.accessToken}` } },
    );
    const memberships: MembershipDTO[] = res.memberships.map((m) => ({
      tenantId: m.tenantId,
      tenantSlug: m.tenantSlug,
      roles: m.roles,
      disabled: m.disabled,
      current: m.current,
    }));
    const out = NextResponse.json({ memberships });
    setSessionCookie(out, tokens.refreshToken, {
      maxAgeSeconds: tokens.refreshExpiresInSeconds,
    });
    return out;
  } catch (err) {
    console.error("[BFF /memberships]", err);
    return NextResponse.json(
      { error: (err as Error).message || "failed to list memberships" },
      { status: 401 },
    );
  }
}
