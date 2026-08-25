import { Code, ConnectError } from "@connectrpc/connect";

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
