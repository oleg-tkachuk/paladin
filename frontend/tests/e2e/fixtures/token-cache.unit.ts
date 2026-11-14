// Unit tests for token-cache.ts, run by vitest's node project. Named .unit.ts
// rather than .test.ts: Playwright's chromium project discovers *.test.ts
// under tests/e2e, fixtures included, and would try to run these as browser
// tests.
import { describe, expect, it } from "vitest";
import { jwtExpiryMs, newTokenCache } from "./token-cache";

const AUDIENCE = "paladin-admin";
const OTHER_AUDIENCE = "paladin-data";
const MARGIN_MS = 60_000;
const LIFETIME_MS = 15 * 60_000;
const CONCURRENT_CALLERS = 12;

/** A login that counts its calls and resolves when `release` is called. */
function gatedLogin() {
  let calls = 0;
  const waiting: Array<() => void> = [];
  return {
    calls: () => calls,
    release: () => waiting.splice(0).forEach((go) => go()),
    login: (audience: string) => {
      calls += 1;
      const n = calls;
      return new Promise<string>((resolve) =>
        waiting.push(() => resolve(`${audience}-token-${n}`)),
      );
    },
  };
}

describe("newTokenCache", () => {
  it("logs in once for callers that ask at the same time", async () => {
    const gate = gatedLogin();
    const get = newTokenCache({
      login: gate.login,
      expiry: () => LIFETIME_MS,
      refreshMarginMs: MARGIN_MS,
      now: () => 0,
    });
    const asked = Array.from({ length: CONCURRENT_CALLERS }, () =>
      get(AUDIENCE),
    );
    gate.release();
    const tokens = await Promise.all(asked);
    expect(gate.calls()).toBe(1);
    expect(new Set(tokens)).toEqual(new Set([`${AUDIENCE}-token-1`]));
  });

  it("keeps a token until the margin before it lapses, then logs in again", async () => {
    let clock = 0;
    const gate = gatedLogin();
    const get = newTokenCache({
      login: gate.login,
      expiry: () => LIFETIME_MS,
      refreshMarginMs: MARGIN_MS,
      now: () => clock,
    });
    const first = get(AUDIENCE);
    gate.release();
    await first;
    clock = LIFETIME_MS - MARGIN_MS - 1;
    expect(await get(AUDIENCE)).toBe(`${AUDIENCE}-token-1`);
    clock = LIFETIME_MS - MARGIN_MS;
    const second = get(AUDIENCE);
    gate.release();
    expect(await second).toBe(`${AUDIENCE}-token-2`);
    expect(gate.calls()).toBe(2);
  });

  it("logs in for each audience on its own", async () => {
    const gate = gatedLogin();
    const get = newTokenCache({
      login: gate.login,
      expiry: () => LIFETIME_MS,
      refreshMarginMs: MARGIN_MS,
      now: () => 0,
    });
    const both = Promise.all([get(AUDIENCE), get(OTHER_AUDIENCE)]);
    gate.release();
    expect(await both).toEqual([
      `${AUDIENCE}-token-1`,
      `${OTHER_AUDIENCE}-token-2`,
    ]);
  });

  it("does not keep a failed login: the next caller tries again", async () => {
    let calls = 0;
    const get = newTokenCache({
      login: async (audience) => {
        calls += 1;
        if (calls === 1) throw new Error("too many attempts");
        return `${audience}-token`;
      },
      expiry: () => LIFETIME_MS,
      refreshMarginMs: MARGIN_MS,
      now: () => 0,
    });
    await expect(get(AUDIENCE)).rejects.toThrow("too many attempts");
    expect(await get(AUDIENCE)).toBe(`${AUDIENCE}-token`);
    expect(calls).toBe(2);
  });
});

describe("jwtExpiryMs", () => {
  const SECONDS = 1_700_000_000;
  const jwt = (payload: object) =>
    `h.${Buffer.from(JSON.stringify(payload)).toString("base64url")}.s`;

  it("reads exp as epoch ms", () => {
    expect(jwtExpiryMs(jwt({ exp: SECONDS }))).toBe(SECONDS * 1000);
  });

  it("reads an unreadable token as already lapsed", () => {
    expect(jwtExpiryMs("not-a-jwt")).toBe(0);
    expect(jwtExpiryMs(jwt({ sub: "x" }))).toBe(0);
  });
});
