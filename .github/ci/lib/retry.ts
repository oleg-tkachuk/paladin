// Calls that reach a public service a release does not control, tried again.
//
// The Sigstore transparency log answered one signing with a 409 conflict, and
// the image it was signing never reached the dev cluster. A call that fails
// every attempt still fails, with its own error.

import { warning } from "./actions.ts";

// RETRY_ATTEMPTS is how many times a call runs before its failure stands;
// RETRY_DELAY_MS, the wait before the second attempt, doubled before each one
// after.
export const RETRY_ATTEMPTS = 4;
export const RETRY_DELAY_MS = 10_000;

export interface RetryOptions {
  attempts?: number;
  delayMs?: number;
  // sleep waits ms; a test records the waits instead.
  sleep?: (ms: number) => Promise<void>;
}

const pause = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

// retry runs fn until it succeeds or has failed attempts times, and returns
// its result; what describes the call in the log.
export async function retry<T>(what: string, fn: () => Promise<T>, options: RetryOptions = {}): Promise<T> {
  const attempts = options.attempts ?? RETRY_ATTEMPTS;
  let delay = options.delayMs ?? RETRY_DELAY_MS;
  for (let attempt = 1; ; attempt++) {
    try {
      return await fn();
    } catch (err) {
      if (attempt >= attempts) {
        throw err;
      }
      warning(`${what}: attempt ${attempt} of ${attempts} failed (${err instanceof Error ? err.message : String(err)}); again in ${delay} ms`);
      await (options.sleep ?? pause)(delay);
      delay *= 2;
    }
  }
}
