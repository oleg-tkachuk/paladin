import { SESSION_PATHS } from "@/constants";

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

const HTTP_UNAUTHORIZED = 401;

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
 * enough, and answers 401 when there is no session. Returns whether there is.
 */
export async function refreshSession(): Promise<boolean> {
  try {
    await post(SESSION_PATHS.refresh);
    return true;
  } catch (err) {
    if (err instanceof SessionError && err.status === HTTP_UNAUTHORIZED)
      return false;
    throw err;
  }
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
