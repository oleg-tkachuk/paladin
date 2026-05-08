import { NextResponse } from "next/server";

import {
  iamAuthClient,
  readSessionCookie,
  clearSessionCookie,
} from "@/lib/auth/bff";

/**
 * POST /api/auth/logout
 *
 * Best-effort revoke of the active refresh chain, then clear the session
 * cookie regardless of the IAM result so a partial failure can't leave
 * the user logged-in client-side.
 */

export const dynamic = "force-dynamic";
export const runtime = "nodejs";

export async function POST(): Promise<NextResponse> {
  const token = await readSessionCookie();
  if (token) {
    try {
      await iamAuthClient().revoke({ token });
    } catch (err) {
      console.warn("[BFF /logout] revoke failed:", err);
    }
  }
  const out = NextResponse.json({ ok: true });
  clearSessionCookie(out);
  return out;
}
