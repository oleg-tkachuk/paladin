import { NextResponse } from "next/server";

import { AUDIENCES } from "@/constants";
import { iamAuthClient, readSessionCookie } from "@/lib/auth/bff";

/**
 * GET /api/audit/stream
 *
 * SSE proxy for the admin plane's live audit feed (/audit/stream on the
 * admin service). The browser's EventSource can't set an Authorization
 * header, so the BFF authenticates from the session cookie: it derives a
 * short-lived admin-audience access token via ExchangeAudience (which does
 * NOT rotate the refresh chain — safe to call per stream connect) and pipes
 * the upstream SSE body through untouched.
 *
 * The upstream stream is tenant-scoped by the token's tenant claim; no
 * client-supplied filtering happens here.
 */

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

const ADMIN_URL = process.env.PALADIN_ADMIN_URL || "http://paladin-core:8090";

export async function GET(req: Request): Promise<Response> {
  const refreshToken = await readSessionCookie();
  if (!refreshToken) {
    return NextResponse.json({ error: "not authenticated" }, { status: 401 });
  }

  let accessToken: string;
  try {
    const res = await iamAuthClient().exchangeAudience({
      refreshToken,
      targetAudience: AUDIENCES.admin,
    });
    accessToken = res.accessToken;
  } catch (err) {
    console.error("[BFF /audit/stream] exchange", err);
    return NextResponse.json(
      { error: (err as Error).message || "exchange failed" },
      { status: 401 },
    );
  }

  let upstream: Response;
  try {
    upstream = await fetch(`${ADMIN_URL}/audit/stream`, {
      headers: {
        Authorization: `Bearer ${accessToken}`,
        Accept: "text/event-stream",
      },
      // Tie the upstream connection to the browser connection so closing the
      // EventSource tears the backend subscription down too.
      signal: req.signal,
      cache: "no-store",
    });
  } catch (err) {
    console.error("[BFF /audit/stream] upstream connect", err);
    return NextResponse.json(
      { error: "upstream unavailable" },
      { status: 502 },
    );
  }
  if (!upstream.ok || !upstream.body) {
    return NextResponse.json(
      { error: `upstream returned ${upstream.status}` },
      { status: upstream.status === 401 ? 401 : 502 },
    );
  }

  return new Response(upstream.body, {
    status: 200,
    headers: {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache, no-transform",
      Connection: "keep-alive",
      "X-Accel-Buffering": "no",
    },
  });
}
