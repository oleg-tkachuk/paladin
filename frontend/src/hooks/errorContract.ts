import { Code, ConnectError } from "@connectrpc/connect";

import { ErrorInfoSchema } from "@/gen/google/rpc/error_details_pb";
import {
  ErrorReason,
  ErrorReasonSchema,
} from "@/gen/paladin/common/v1/error_reason_pb";

/** The ErrorInfo domain the server marks its own reasons with. */
export const PALADIN_ERROR_DOMAIN = "paladin";

/**
 * Data-hook error contract — the ONE rule every hook in this directory
 * follows. See src/hooks/README.md for the rationale.
 *
 *   QUERIES (fetch* / refresh / loadMore)  → STATE-ONLY.
 *     On failure they call setError(msg) and return a safe sentinel
 *     (empty list / unchanged state). They do NOT throw. Callers render
 *     the { data, loading, error } triad; they never wrap a query in
 *     try/catch.
 *
 *   MUTATIONS (create / update / delete / restore / purge / set*) →
 *     THROW-ONLY. On failure they throw the original error and do NOT
 *     touch the shared `error` state (that state is the query render
 *     channel; a failed create must not light up the list's error
 *     banner). Callers `await` inside try/catch and surface the message
 *     via errorMessage(err) — a toast, usually.
 *
 * This helper is the shared message extractor so callers (and the
 * mutation paths themselves, for toasts) read a clean string off any
 * thrown value without re-implementing the ConnectError unwrap.
 */
export function errorMessage(
  err: unknown,
  fallback = "Something went wrong",
): string {
  if (err instanceof ConnectError) {
    return err.rawMessage;
  }
  if (err instanceof Error && err.message) {
    return err.message;
  }
  return fallback;
}

/**
 * True when a request failed because it was cancelled, not because anything
 * went wrong: TanStack aborts an in-flight query when the component unmounts
 * or a newer fetch supersedes it, and the transport surfaces that as
 * Code.Canceled (or a DOMException AbortError).
 *
 * Worth its own helper because the page-level queryFns toast on failure, and
 * an abort toasted as "Load failed — signal is aborted without reason". That
 * is noise on every navigation, and on the budget page the toast landed on top
 * of the submit button and swallowed the click.
 */
export function isAbortError(err: unknown): boolean {
  if (err instanceof ConnectError) {
    return err.code === Code.Canceled;
  }
  return err instanceof DOMException && err.name === "AbortError";
}

/**
 * Why a call failed, beyond its code: the ErrorReason the server attaches to
 * every error it maps from a domain condition, in a google.rpc.ErrorInfo of
 * the "paladin" domain. UNSPECIFIED for anything else — a transport failure,
 * another domain's detail, or a reason newer than this console, which is
 * read as the code alone.
 *
 * For deciding what to offer (a restore, a link to probe a backend), never
 * for the text: errorMessage stays the message.
 */
export function errorReason(err: unknown): ErrorReason {
  if (!(err instanceof ConnectError)) {
    return ErrorReason.UNSPECIFIED;
  }
  for (const info of err.findDetails(ErrorInfoSchema)) {
    if (info.domain !== PALADIN_ERROR_DOMAIN) {
      continue;
    }
    const value = ErrorReasonSchema.values.find((v) => v.name === info.reason);
    if (value) {
      return value.number as ErrorReason;
    }
  }
  return ErrorReason.UNSPECIFIED;
}
