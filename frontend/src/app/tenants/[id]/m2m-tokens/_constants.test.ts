import { describe, expect, it } from "vitest";

import type { APIToken } from "@/gen/paladin/admin/v1/api_token_service_pb";

import { tokenLifecycle } from "./_constants";

const HOUR_S = 3600n;
const nowS = () => BigInt(Math.floor(Date.now() / 1000));
const token = (t: { revokedAt?: bigint; expiresAt?: bigint }) =>
  ({
    revokedAt: t.revokedAt === undefined ? undefined : { seconds: t.revokedAt },
    expiresAt: t.expiresAt === undefined ? undefined : { seconds: t.expiresAt },
  }) as unknown as APIToken;

describe("tokenLifecycle", () => {
  it.each([
    ["a token with no expiry", {}, "active"],
    ["a token yet to expire", { expiresAt: nowS() + HOUR_S }, "active"],
    ["a token past its expiry", { expiresAt: nowS() - HOUR_S }, "expired"],
    ["a revoked token", { revokedAt: nowS() - HOUR_S }, "revoked"],
    [
      "a revoked token since expired",
      { revokedAt: nowS() - HOUR_S, expiresAt: nowS() - 1n },
      "revoked",
    ],
  ] as const)("names %s %s", (_, t, want) => {
    expect(tokenLifecycle(token(t))).toBe(want);
  });
});
