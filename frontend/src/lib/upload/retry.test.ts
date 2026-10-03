import { afterEach, describe, expect, it, vi } from "vitest";

import {
  PRESIGN_EXPIRY_SKEW_MS,
  TRANSFER_ATTEMPTS,
  TransferError,
  alreadyStored,
  expired,
  msUntilStale,
  retryTiming,
  retryable,
  usable,
  withRetries,
} from "./retry";

const EXPIRED_BODY =
  "<Error><Code>AccessDenied</Code><Message>Request has expired</Message></Error>";
const NOW = Date.parse("2027-01-15T08:00:00Z");
const at = (offsetMs: number) => new Date(NOW + offsetMs).toISOString();

afterEach(() => vi.restoreAllMocks());

describe("classification", () => {
  it.each([
    ["expired", new TransferError(403, EXPIRED_BODY, ""), true, false, true],
    [
      "bad signature",
      new TransferError(403, "SignatureDoesNotMatch", ""),
      false,
      false,
      false,
    ],
    ["stored", new TransferError(412, "", ""), false, true, false],
    ["busy", new TransferError(503, "", ""), false, false, true],
    ["no answer", new TransferError(0, "", ""), false, false, true],
    ["refused", new TransferError(400, "", ""), false, false, false],
    ["a bug", new Error("x"), false, false, false],
  ])("%s", (_, err, isExpired, isStored, isRetryable) => {
    expect(expired(err)).toBe(isExpired);
    expect(alreadyStored(err)).toBe(isStored);
    expect(retryable(err)).toBe(isRetryable);
  });
});

describe("usable", () => {
  it.each([
    ["no expiry", "", true],
    ["unreadable", "not a time", true],
    ["expired", at(0), false],
    ["inside the skew", at(PRESIGN_EXPIRY_SKEW_MS - 1000), false],
    ["past the skew", at(PRESIGN_EXPIRY_SKEW_MS + 1000), true],
  ])("%s", (_, expiresAtRfc3339, want) => {
    expect(usable({ expiresAtRfc3339 }, NOW)).toBe(want);
  });
});

describe("msUntilStale", () => {
  it("is the time left before the skew, never negative", () => {
    const left = 60_000;
    expect(
      msUntilStale(
        { expiresAtRfc3339: at(PRESIGN_EXPIRY_SKEW_MS + left) },
        NOW,
      ),
    ).toBe(left);
    expect(msUntilStale({ expiresAtRfc3339: at(0) }, NOW)).toBe(0);
    expect(msUntilStale({ expiresAtRfc3339: "" }, NOW)).toBeUndefined();
  });
});

describe("backoff", () => {
  it("doubles up to its cap", () => {
    expect([1, 2, 3].map(retryTiming.backoffMs)).toEqual([200, 400, 800]);
    expect(retryTiming.backoffMs(64)).toBe(5_000);
  });
});

describe("withRetries", () => {
  const signed = (url: string) => ({ url, expiresAtRfc3339: "" }) as never;

  it("presigns before sending a URL that is about to expire", async () => {
    const presign = vi.fn(async () => signed("fresh"));
    const send = vi.fn(async (s: { url: string }) => s.url);
    const stale = { url: "stale", expiresAtRfc3339: new Date().toISOString() };
    expect(await withRetries(stale as never, presign, send)).toBe("fresh");
    expect(send).toHaveBeenCalledTimes(1);
  });

  it("stops at its attempts", async () => {
    vi.spyOn(retryTiming, "backoffMs").mockReturnValue(0);
    const send = vi.fn(async () => {
      throw new TransferError(503, "", "busy");
    });
    await expect(
      withRetries(undefined, async () => signed("u"), send),
    ).rejects.toThrow("busy");
    expect(send).toHaveBeenCalledTimes(TRANSFER_ATTEMPTS);
  });
});
