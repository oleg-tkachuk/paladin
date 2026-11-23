import { Code, ConnectError } from "@connectrpc/connect";

/**
 * What the console makes of the session role's answer when it checks the
 * session: signed in, signed out, or not known right now. Pure, so the
 * session client, AuthContext and the tests read an answer the same way.
 *
 * Only a refusal signs the page out. A role that cannot check the session —
 * the keys that verify it would not load, IAM restarting, the network down —
 * says nothing of the session, and the page keeps it and asks again.
 */
export type SessionCheck =
  | { kind: "signed-in" }
  | { kind: "signed-out" }
  /** retryAfterMs is the wait the role asked for, or null when it named none. */
  | { kind: "unavailable"; retryAfterMs: number | null };

/** A check that did not find a session: signed out, or not known right now. */
export type SessionMiss = Exclude<SessionCheck, { kind: "signed-in" }>;

/** The role's answer: its status and Retry-After, or no answer at all. */
export type SessionAnswer =
  { status: number; retryAfter: string | null } | { networkError: true };

/** What the console shows while it cannot check the session. */
export const SESSION_UNAVAILABLE_MESSAGE =
  "Sign-in is temporarily unavailable, retrying.";

/** The header in which the role names how long to wait before asking again. */
export const RETRY_AFTER_HEADER = "Retry-After";

const HTTP_SUCCESS_MIN = 200;
const HTTP_SUCCESS_MAX = 299;
const HTTP_TOO_MANY_REQUESTS = 429;
const HTTP_SERVER_ERROR_MIN = 500;
const MS_PER_SECOND = 1_000;
/** Retry-After as delay-seconds (RFC 9110 §10.2.3). */
const DELAY_SECONDS = /^\d+$/;
/** Retry-After as an HTTP date, which spells out its day and month. */
const HTTP_DATE_HAS_NAMES = /[A-Za-z]/;

/** The first wait between checks, doubled after each that is unavailable. */
export const SESSION_RETRY_BASE_MS = 1_000;
/** The longest wait between checks, Retry-After included. */
export const SESSION_RETRY_MAX_MS = 30_000;

/** Connect codes that say the server could not decide, not that it refused. */
const UNAVAILABLE_CODES: ReadonlySet<Code> = new Set([
  Code.Unavailable,
  Code.DeadlineExceeded,
  Code.ResourceExhausted,
  Code.Internal,
  // connect-web's code for a call that never got an answer.
  Code.Unknown,
]);

const unavailable = (retryAfter: string | null, now: number): SessionMiss => ({
  kind: "unavailable",
  retryAfterMs: parseRetryAfter(retryAfter, now),
});

/**
 * Classifies the role's answer to a session check. A success is a session;
 * 429 and any 5xx are a server that could not decide; any other refusal —
 * 401 above all — is no session.
 */
export function classifySessionAnswer(
  answer: SessionAnswer,
  now: number = Date.now(),
): SessionCheck {
  if ("networkError" in answer) return unavailable(null, now);
  const { status } = answer;
  if (status >= HTTP_SUCCESS_MIN && status <= HTTP_SUCCESS_MAX)
    return { kind: "signed-in" };
  if (status === HTTP_TOO_MANY_REQUESTS || status >= HTTP_SERVER_ERROR_MIN)
    return unavailable(answer.retryAfter, now);
  return { kind: "signed-out" };
}

/**
 * Classifies a call that failed after the session check found a session —
 * WhoAmI through the session transport. A call that never got an answer, or
 * that the server could not decide, leaves the session unknown; any other
 * failure, Unauthenticated above all, is no session.
 */
export function classifySessionError(
  err: unknown,
  now: number = Date.now(),
): SessionMiss {
  if (err instanceof ConnectError) {
    if (UNAVAILABLE_CODES.has(err.code))
      return unavailable(err.metadata.get(RETRY_AFTER_HEADER), now);
    return { kind: "signed-out" };
  }
  // fetch rejects with a TypeError when the request never got an answer.
  if (err instanceof TypeError) return unavailable(null, now);
  return { kind: "signed-out" };
}

/**
 * The wait a Retry-After names, in seconds or as an HTTP date, capped at
 * SESSION_RETRY_MAX_MS; null for none or one that cannot be read.
 */
export function parseRetryAfter(
  value: string | null,
  now: number = Date.now(),
): number | null {
  const text = value?.trim();
  if (!text) return null;
  let ms: number;
  if (DELAY_SECONDS.test(text)) {
    ms = Number(text) * MS_PER_SECOND;
  } else {
    // An HTTP date names its day and month; Date.parse alone would read a
    // bare "-1" as a year.
    if (!HTTP_DATE_HAS_NAMES.test(text)) return null;
    const at = Date.parse(text);
    if (Number.isNaN(at)) return null;
    ms = Math.max(0, at - now);
  }
  return Math.min(ms, SESSION_RETRY_MAX_MS);
}

/**
 * How long to wait before check number attempt + 1 (attempt counts from 0):
 * SESSION_RETRY_BASE_MS doubled per attempt, capped at SESSION_RETRY_MAX_MS,
 * and never shorter than the Retry-After the role asked for.
 */
export function sessionRetryDelayMs(
  attempt: number,
  retryAfterMs: number | null,
): number {
  const backoff = Math.min(
    SESSION_RETRY_BASE_MS * 2 ** attempt,
    SESSION_RETRY_MAX_MS,
  );
  return Math.max(backoff, retryAfterMs ?? 0);
}
