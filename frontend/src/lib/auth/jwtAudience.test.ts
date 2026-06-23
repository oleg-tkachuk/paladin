import { describe, it, expect } from "vitest";

import { tokenMatchesPlane } from "./jwtAudience";

// Build a JWT with the given `aud` claim. Only the payload segment
// matters here (the gate decodes, never verifies — the backend verifies).
function jwt(aud: unknown): string {
  const b64url = (o: unknown) =>
    Buffer.from(JSON.stringify(o))
      .toString("base64")
      .replace(/\+/g, "-")
      .replace(/\//g, "_")
      .replace(/=+$/, "");
  return `${b64url({ alg: "RS256" })}.${b64url({ aud })}.sig`;
}

describe("tokenMatchesPlane", () => {
  it("allows an absent token (anonymous flows decided downstream)", () => {
    expect(tokenMatchesPlane(null, "admin")).toBe(true);
  });

  it("accepts a token whose aud matches the plane", () => {
    expect(tokenMatchesPlane(`Bearer ${jwt("paladin-admin")}`, "admin")).toBe(true);
    expect(tokenMatchesPlane(jwt("paladin-data"), "data")).toBe(true);
  });

  it("rejects a token aimed at the wrong plane (confused-deputy)", () => {
    expect(tokenMatchesPlane(`Bearer ${jwt("paladin-data")}`, "admin")).toBe(false);
  });

  it("supports aud as an array per RFC 7519", () => {
    expect(tokenMatchesPlane(jwt(["paladin-iam", "paladin-admin"]), "admin")).toBe(
      true,
    );
  });

  it("rejects a malformed token", () => {
    expect(tokenMatchesPlane("Bearer not-a-jwt", "data")).toBe(false);
    expect(tokenMatchesPlane("a.b", "data")).toBe(false);
  });

  it("rejects a token with no aud claim", () => {
    const noAud = `${Buffer.from('{"alg":"RS256"}').toString("base64url")}.${Buffer.from("{}").toString("base64url")}.sig`;
    expect(tokenMatchesPlane(noAud, "data")).toBe(false);
  });
});
