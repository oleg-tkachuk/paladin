import { createConnectTransport } from "@connectrpc/connect-web";
import { anyRegistry } from "./any-registry";

import {
  ConnectError,
  Code,
  type Interceptor,
  type Transport,
} from "@connectrpc/connect";

import { kNotFoundIsAnswer } from "./expected";
import { SESSION_RPC_PREFIX } from "@/constants";
import { signalSessionEnded } from "@/lib/session/ended";

/**
 * The console's one transport (ADR-0048). Every call goes to the session
 * role under SESSION_RPC_PREFIX with the session cookie and nothing else: the
 * role attaches the access token the call's plane needs, picking the plane by
 * the procedure's proto package. The browser holds no token.
 *
 * Tenant resolution: the backend reads the tenant from the token the session
 * role attaches. The UI never sends X-Tenant-ID.
 */

/**
 * Idempotency-Key auto-injection, decided by the wire contract.
 *
 * This was a prefix list — Create, Issue, Delegate, Grant, Upload, Complete,
 * Initiate, Copy, Restore, Batch — duplicated verbatim in
 * `backend/cmd/seed-fixture/main.go` and `backend/internal/mcp/idempotency.go`,
 * each with a comment telling the reader to change the other two. The proto now
 * carries the answer as `idempotency_level` on 112 of 142 RPCs, and
 * `@bufbuild/protobuf` exposes it at runtime as `req.method.idempotency`.
 *
 * The rule: stamp a key iff the method declares IDEMPOTENCY_UNKNOWN.
 *
 *   - NO_SIDE_EFFECTS changes nothing. A key would write a row in
 *     idempotency_keys and risk serving a snapshot from up to a TTL ago in
 *     answer to a question about now.
 *   - IDEMPOTENT is already safe to repeat — by resource_version, by being a
 *     removal, or by upserting the value the caller supplied. A key adds a row
 *     and no safety.
 *   - IDEMPOTENCY_UNKNOWN is every call whose repeat nobody has promised
 *     anything about, which is exactly where the key does its work.
 *
 * Two behaviours change, and both are the descriptor being right where the
 * prefixes were not. `RestoreObjectVersion` no longer gets a key: the `Restore`
 * prefix swept it in, and it is OCC-guarded. Credential minting — Login,
 * RefreshToken, ExchangeAudience, SwitchTenant — now DOES get one: the console
 * used to withhold it because replaying a rotated refresh token is wrong, which
 * is true and is enforced where it belongs, on the server
 * (middleware.CredentialMintingProcedures). A key is a caller's de-duplication
 * token, not a request to cache; the server decides, and for those four it
 * declines.
 *
 * A fresh UUID per call, so a transport-level retry of the same request object
 * collapses. It does NOT collapse two separate user clicks — those are two
 * calls and get two keys. Making a double-click idempotent needs a key derived
 * at the call site from the submission, which is a call-site decision, not a
 * transport one.
 */
export function wantsIdempotencyKey(method: { idempotency?: number }): boolean {
  // 0 is IDEMPOTENCY_UNKNOWN in google.protobuf.MethodOptions. A method
  // descriptor without the field at all is treated the same way: the generated
  // code always sets it, so `undefined` means the caller passed something that
  // is not a method descriptor, and defaulting to "send a key" keeps a
  // mis-wired call safe rather than silently unkeyed.
  return (method.idempotency ?? 0) === 0;
}

const idempotencyInterceptor: Interceptor = (next) => async (req) => {
  if (!wantsIdempotencyKey(req.method) || req.header.has("Idempotency-Key")) {
    return next(req);
  }
  // Three request messages carry an `idempotency_key` FIELD of their own —
  // UploadObject, InitiateMultipartUpload, and the capability issue/delegate
  // pair. The server resolves the key from the header OR that field, and
  // rejects the call outright when both are set and differ:
  //
  //     invalid_argument: Idempotency-Key header and idempotency_key field disagree
  //
  // which is right — a caller contradicting itself is a bug, and picking a
  // winner would make the effective key depend on an undocumented precedence.
  // The first version of this interceptor stamped a fresh UUID over the field
  // the console already sets per queued file, and every console upload started
  // failing with that 400.
  //
  // So mirror the field instead of inventing a value. Both sides then say the
  // same thing, and the key is the one the CALL SITE chose — stable across a
  // real double-submit, where a per-call UUID only ever collapses a
  // transport-level retry.
  const carried = (req.message as { idempotencyKey?: unknown }).idempotencyKey;
  req.header.set(
    "Idempotency-Key",
    typeof carried === "string" && carried !== ""
      ? carried
      : crypto.randomUUID(),
  );
  return next(req);
};

// A request aborted by the caller (TanStack Query cancels a query's RPC on
// unmount / key-change; React StrictMode double-mounts make this routine in
// dev) is expected, not an error — surfaces as ConnectError Code.Canceled or
// a raw AbortError. Don't log those as `[RPC Error]`.
function isCanceled(err: unknown): boolean {
  if (err instanceof ConnectError) return err.code === Code.Canceled;
  return err instanceof DOMException && err.name === "AbortError";
}

const loggingInterceptor: Interceptor = (next) => async (req) => {
  try {
    return await next(req);
  } catch (err: unknown) {
    if (err instanceof ConnectError && err.code === Code.Unimplemented) {
      throw err;
    }
    if (isCanceled(err)) {
      throw err;
    }
    if (
      err instanceof ConnectError &&
      err.code === Code.NotFound &&
      req.contextValues.get(kNotFoundIsAnswer)
    ) {
      throw err;
    }
    console.error(`[RPC Error] ${req.method.name}:`, err);
    throw err;
  }
};

/**
 * A call the session role answers Unauthenticated has no session behind it:
 * the cookie expired or was signed out elsewhere. The app is told once, and
 * the error still reaches the caller.
 */
const sessionInterceptor: Interceptor = (next) => async (req) => {
  try {
    return await next(req);
  } catch (err) {
    if (err instanceof ConnectError && err.code === Code.Unauthenticated) {
      signalSessionEnded();
    }
    throw err;
  }
};

export const sessionTransport: Transport = createConnectTransport({
  baseUrl: SESSION_RPC_PREFIX,
  jsonOptions: { registry: anyRegistry },
  interceptors: [
    idempotencyInterceptor,
    loggingInterceptor,
    sessionInterceptor,
  ],
});

// The plane names stay for the clients built on them (lib/connect/client.ts);
// the session role routes each call to its plane itself.
export const dataTransport = sessionTransport;
export const iamTransport = sessionTransport;
export const adminTransport = sessionTransport;
