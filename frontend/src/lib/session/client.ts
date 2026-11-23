import { SESSION_PATHS } from "@/constants";

import {
  RETRY_AFTER_HEADER,
  classifySessionAnswer,
  type SessionCheck,
} from "./check";

/**
 * The session role's own endpoints (ADR-0048): sign-in, rotation, the tenant
 * switch and sign-out. Each sets or clears the session cookie in its answer;
 * none hands the page a token. Everything else the console calls goes through
 * the session transport (lib/connect/transport.ts).
 */

/** A session endpoint's refusal, in the Connect error shape it answers with. */
export class SessionError extends Error {
  constructor(
    message: string,
    readonly code: string,
    readonly status: number,
  ) {
    super(message);
    this.name = "SessionError";
  }
}

async function post(path: string, body?: unknown): Promise<Response> {
  const res = await fetch(path, {
    method: "POST",
    credentials: "same-origin",
    headers:
      body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.ok) return res;
  const err = (await res.json().catch(() => ({}))) as {
    code?: string;
    message?: string;
  };
  throw new SessionError(
    err.message || `${path} failed (${res.status})`,
    err.code || "unknown",
    res.status,
  );
}

/** Signs in; the role sets the session cookie. */
export async function signIn(subject: string, password: string): Promise<void> {
  await post(SESSION_PATHS.login, { subject, password });
}

/**
 * Keeps the session alive: the role rotates its refresh token once it is old
 * enough. Answers what the check found — a session, none (401), or a role that
 * could not check it right now (503, or no answer) — as classifySessionAnswer
 * reads it; it never throws.
 */
export async function refreshSession(): Promise<SessionCheck> {
  let res: Response;
  try {
    res = await fetch(SESSION_PATHS.refresh, {
      method: "POST",
      credentials: "same-origin",
    });
  } catch {
    return classifySessionAnswer({ networkError: true });
  }
  return classifySessionAnswer({
    status: res.status,
    retryAfter: res.headers.get(RETRY_AFTER_HEADER),
  });
}

/** Re-scopes the session to another tenant the user belongs to. */
export async function switchSessionTenant(
  targetTenantId: string,
): Promise<void> {
  await post(SESSION_PATHS.switchTenant, { targetTenantId });
}

/** Signs out; the role revokes the session and clears the cookie. */
export async function signOut(): Promise<void> {
  await post(SESSION_PATHS.logout);
}
