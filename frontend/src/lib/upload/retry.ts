import type { PresignedUrl } from "@/gen/paladin/common/v1/resource_pb";

/**
 * Retries for presigned requests. A failed request is sent again through a
 * freshly presigned URL, so an attempt never reuses one that expired or was
 * refused; a request that cannot succeed by repeating — a 4xx other than an
 * expiry — is not retried. The same policy as the SDKs.
 */

/** How many times one presigned request is sent before its error is shown. */
export const TRANSFER_ATTEMPTS = 4;

/** How close to its expiry a URL is presigned again instead of sent: room
 *  for clock skew and for the request itself. */
export const PRESIGN_EXPIRY_SKEW_MS = 30_000;

const BACKOFF_BASE_MS = 200;
const BACKOFF_MAX_MS = 5_000;

const HTTP_FORBIDDEN = 403;
const HTTP_PRECONDITION_FAILED = 412;

/** Statuses after which the store was busy or down, not refusing this
 *  request. */
const RETRYABLE_STATUS = new Set([408, 429, 500, 502, 503, 504]);

/** In what S3-compatible stores answer when a presigned URL, or the session
 *  credentials that signed it, has expired ("Request has expired",
 *  "ExpiredToken"). */
const EXPIRED_MARKER = "expired";

/** A presigned request storage refused, or that never got an answer (status
 *  0: the network failed, or CORS hid the response). */
export class TransferError extends Error {
  constructor(
    readonly status: number,
    readonly body: string,
    message: string,
  ) {
    super(message);
    this.name = "TransferError";
  }
}

/** Storage refused a presigned URL because it expired. */
export function expired(err: unknown): boolean {
  return (
    err instanceof TransferError &&
    err.status === HTTP_FORBIDDEN &&
    err.body.toLowerCase().includes(EXPIRED_MARKER)
  );
}

/** Storage refused an upload because an object is already at the key — the
 *  URL's If-None-Match held. After a PUT whose answer was lost, that means
 *  the first attempt landed. */
export function alreadyStored(err: unknown): boolean {
  return (
    err instanceof TransferError && err.status === HTTP_PRECONDITION_FAILED
  );
}

/** Whether sending again, through a fresh URL, may succeed. */
export function retryable(err: unknown): boolean {
  if (!(err instanceof TransferError)) return false;
  return err.status === 0 || RETRYABLE_STATUS.has(err.status) || expired(err);
}

/** Whether a URL still has PRESIGN_EXPIRY_SKEW_MS left; one with no expiry,
 *  or an unreadable one, is taken as usable. */
export function usable(
  signed: Pick<PresignedUrl, "expiresAtRfc3339">,
  now: number = Date.now(),
): boolean {
  if (!signed.expiresAtRfc3339) return true;
  const at = Date.parse(signed.expiresAtRfc3339);
  if (Number.isNaN(at)) return true;
  return at - now > PRESIGN_EXPIRY_SKEW_MS;
}

/** Milliseconds until a URL should be presigned again; 0 when it already
 *  should, undefined when it has no expiry. */
export function msUntilStale(
  signed: Pick<PresignedUrl, "expiresAtRfc3339">,
  now: number = Date.now(),
): number | undefined {
  if (!signed.expiresAtRfc3339) return undefined;
  const at = Date.parse(signed.expiresAtRfc3339);
  if (Number.isNaN(at)) return undefined;
  return Math.max(0, at - now - PRESIGN_EXPIRY_SKEW_MS);
}

/** The pause before a retry; a field so tests can make it instant. */
export const retryTiming = {
  backoffMs(attempt: number): number {
    return Math.min(BACKOFF_BASE_MS * 2 ** (attempt - 1), BACKOFF_MAX_MS);
  },
};

/**
 * Sends through `first` — presigning again when it is near its expiry — and
 * on a retryable failure waits, presigns a fresh URL and sends again, up to
 * TRANSFER_ATTEMPTS times. `send` must be safe to repeat.
 */
export async function withRetries<T>(
  first: PresignedUrl | undefined,
  presign: () => Promise<PresignedUrl>,
  send: (signed: PresignedUrl) => Promise<T>,
): Promise<T> {
  let signed = first;
  for (let attempt = 1; ; attempt++) {
    if (!signed || !usable(signed)) signed = await presign();
    try {
      return await send(signed);
    } catch (err) {
      if (attempt >= TRANSFER_ATTEMPTS || !retryable(err)) throw err;
    }
    await new Promise((resolve) =>
      setTimeout(resolve, retryTiming.backoffMs(attempt)),
    );
    signed = undefined;
  }
}
