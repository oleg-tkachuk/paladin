import { SignJWT } from "jose";
import { describe, expect, it } from "vitest";

import { ROTATE_AFTER_SECONDS, rotationDue } from "./rotation";

// Any key will do: rotationDue reads the claims and never verifies.
const KEY = new TextEncoder().encode("test-key-not-a-secret-0123456789");
const NOW_S = 1_790_000_000;
const NOW_MS = NOW_S * 1000;

const tokenIssuedAt = (iat?: number) => {
  const jwt = new SignJWT({}).setProtectedHeader({ alg: "HS256" });
  if (iat !== undefined) jwt.setIssuedAt(iat);
  return jwt.sign(KEY);
};

describe("rotationDue", () => {
  it.each([
    ["just issued", NOW_S, false],
    ["a second short of due", NOW_S - ROTATE_AFTER_SECONDS + 1, false],
    ["exactly due", NOW_S - ROTATE_AFTER_SECONDS, true],
    ["long past due", NOW_S - 10 * ROTATE_AFTER_SECONDS, true],
  ])("%s", async (_, iat, due) => {
    expect(rotationDue(await tokenIssuedAt(iat), NOW_MS)).toBe(due);
  });

  it("rotates a token without iat, as /me always did", async () => {
    expect(rotationDue(await tokenIssuedAt(), NOW_MS)).toBe(true);
  });

  it("rotates a token it cannot decode", () => {
    expect(rotationDue("not-a-jwt", NOW_MS)).toBe(true);
  });
});
