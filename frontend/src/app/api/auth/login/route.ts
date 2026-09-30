import { NextResponse } from "next/server";

import { AUDIENCES, type Audience } from "@/constants";
import {
  iamAuthClient,
  setSessionCookie,
  toUserDTO,
  toAccessTokenDTO,
  type AccessTokenDTO,
  type UserDTO,
} from "@/lib/auth/bff";
import { loginFailure } from "@/lib/auth/loginFailure";

/**
 * POST /api/auth/login
 * Body: { subject, password, upstreamCode? }
 *
 * Single Login call against the IAM plane (audience=paladin-iam). The
 * iam-bound refresh token goes into the only session cookie; access
 * tokens for paladin-data / paladin-admin are derived on demand by /api/auth/
 * exchange via AuthService.ExchangeAudience.
 *
 * The browser receives a primed iam access token + the user record so
 * the AuthContext can render identity immediately and the data/admin
 * tokens lazy-fetch on first plane RPC.
 */

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

type LoginRequestBody = {
  subject?: string;
  password?: string;
  upstreamCode?: string;
};

type LoginResponseBody = {
  user: UserDTO;
  accessTokens: AccessTokenDTO[];
};

export async function POST(req: Request): Promise<NextResponse> {
  let body: LoginRequestBody;
  try {
    body = await req.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON" }, { status: 400 });
  }

  const subject = body.subject?.trim() ?? "";
  const password = body.password ?? "";
  const upstreamCode = body.upstreamCode ?? "";
  if (!subject || (!password && !upstreamCode)) {
    return NextResponse.json(
      { error: "subject and password (or upstreamCode) are required" },
      { status: 400 },
    );
  }

  const iamAudience: Audience = AUDIENCES.iam;
  try {
    const res = await iamAuthClient().login({
      subject,
      password,
      upstreamCode,
      requestedAudience: iamAudience,
    });
    const tokens = res.tokens;
    if (!tokens) {
      return NextResponse.json(
        { error: "IAM Login returned no token pair" },
        { status: 502 },
      );
    }
    if (!res.user) {
      return NextResponse.json(
        { error: "IAM Login returned no user" },
        { status: 502 },
      );
    }

    const payload: LoginResponseBody = {
      user: toUserDTO(res.user),
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
    console.error("[BFF /login]", err);
    const { status, error } = loginFailure(err);
    return NextResponse.json({ error }, { status });
  }
}
