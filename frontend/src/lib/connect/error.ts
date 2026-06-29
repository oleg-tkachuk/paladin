import { ConnectError } from "@connectrpc/connect";

/**
 * Normalises any thrown value into an Error with a clean message — a
 * ConnectError surfaces its `rawMessage` (the server detail without the
 * `[code]` prefix), everything else passes through or is stringified.
 *
 * Shared by the data hooks so a TanStack Query `queryFn` can `throw
 * normalizeError(err)` and have `query.error` already be a friendly Error.
 */
export function normalizeError(err: unknown): Error {
  if (err instanceof ConnectError) return new Error(err.rawMessage);
  if (err instanceof Error) return err;
  return new Error(String(err));
}
