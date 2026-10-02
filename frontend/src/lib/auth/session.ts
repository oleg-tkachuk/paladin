import { NextResponse } from "next/server";

import { AUDIENCES } from "@/constants";

import { iamAuthClient, readSessionCookie } from "./bff";

/** The body a route returns when the caller has no valid session. */
export const NO_SESSION = { error: "not authenticated" } as const;

/**
 * Proves the caller holds a live session, for BFF routes the proxy leaves
 * public. proxy.ts only checks that the session cookie exists, which anyone
 * can fake; IAM exchanging it for an iam-audience token is what shows it is
 * real. Exchange does not rotate the refresh chain, so a route may call this
 * on every request.
 *
 * Returns null when the session is valid, or the 401 response to send.
 */
export async function requireSession(): Promise<NextResponse | null> {
  const refreshToken = await readSessionCookie();
  if (!refreshToken) {
    return NextResponse.json(NO_SESSION, { status: 401 });
  }
  try {
    await iamAuthClient().exchangeAudience({
      refreshToken,
      targetAudience: AUDIENCES.iam,
    });
  } catch {
    return NextResponse.json(NO_SESSION, { status: 401 });
  }
  return null;
}
