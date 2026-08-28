import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const h = vi.hoisted(() => ({
  exchangeAudience: vi.fn(),
  readSessionCookie: vi.fn(),
  refreshIamChain: vi.fn(),
}));

vi.mock("@/lib/auth/bff", async (orig) => {
  const actual = await orig<typeof import("@/lib/auth/bff")>();
  return {
    ...actual,
    iamAuthClient: () => ({ exchangeAudience: h.exchangeAudience }),
    readSessionCookie: h.readSessionCookie,
    refreshIamChain: h.refreshIamChain,
  };
});

import { POST } from "./route";
import { SESSION_COOKIE_NAME } from "@/lib/auth/bff";

function exchangeReq(body: unknown) {
  return new Request("https://app.test/api/auth/exchange", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: typeof body === "string" ? body : JSON.stringify(body),
  });
}

describe("POST /api/auth/exchange", () => {
  beforeEach(() => {
    h.exchangeAudience.mockReset();
    h.readSessionCookie.mockReset();
    h.refreshIamChain.mockReset();
    vi.spyOn(console, "error").mockImplementation(() => {});
  });
  afterEach(() => vi.restoreAllMocks());

  it("400s on an unknown audience", async () => {
    h.readSessionCookie.mockResolvedValue("rt");
    const res = await POST(exchangeReq({ audience: "paladin-bogus" }));
    expect(res.status).toBe(400);
  });

  it("401s when there is no session cookie", async () => {
    h.readSessionCookie.mockResolvedValue(null);
    const res = await POST(exchangeReq({ audience: "paladin-admin" }));
    expect(res.status).toBe(401);
  });

  // The iam audience is derived like any other now. It used to rotate, which
  // is what put two rotations of one cookie on a single page load — this one
  // and /api/auth/me's — with the browser unable to update the cookie between
  // them. The assertion that matters is the negative one: this route consumes
  // nothing and writes no cookie.
  it("iam audience derives a token without rotating or writing the cookie", async () => {
    h.readSessionCookie.mockResolvedValue("rt-old");
    h.exchangeAudience.mockResolvedValue({
      accessToken: "iam-access",
      accessExpiresInSeconds: 900,
    });
    const res = await POST(exchangeReq({ audience: "paladin-iam" }));
    expect(res.status).toBe(200);
    const json = await res.json();
    expect(json.audience).toBe("paladin-iam");
    expect(json.token).toBe("iam-access");
    expect(h.exchangeAudience).toHaveBeenCalledWith({
      refreshToken: "rt-old",
      targetAudience: "paladin-iam",
    });
    expect(h.refreshIamChain).not.toHaveBeenCalled();
    expect(res.cookies.get(SESSION_COOKIE_NAME)).toBeUndefined();
  });

  it("data audience derives a token via ExchangeAudience and leaves the cookie untouched", async () => {
    h.readSessionCookie.mockResolvedValue("rt");
    h.exchangeAudience.mockResolvedValue({
      accessToken: "data-access",
      accessExpiresInSeconds: 300,
    });
    const res = await POST(exchangeReq({ audience: "paladin-data" }));
    expect(res.status).toBe(200);
    const json = await res.json();
    expect(json.audience).toBe("paladin-data");
    expect(json.token).toBe("data-access");
    expect(h.refreshIamChain).not.toHaveBeenCalled();
    expect(res.cookies.get(SESSION_COOKIE_NAME)).toBeUndefined();
  });

  // The self-heal that used to be tested here — catch Unauthenticated,
  // re-read the cookie, follow the in-process rotation index, retry once — is
  // gone with the code. A token a sibling rotation just consumed is now
  // honoured by the server inside a grace window
  // (refresh_tokens.superseded_at), so the BFF sends what it was given and
  // does not retry. What replaced the test lives in
  // backend/internal/api/iam/v1/authh/supersession_test.go, which is where the
  // decision is now made.
  it("sends the cookie as given, without a retry", async () => {
    h.readSessionCookie.mockResolvedValue("rt-possibly-rotated");
    h.exchangeAudience.mockResolvedValue({
      accessToken: "data-access",
      accessExpiresInSeconds: 300,
    });

    const res = await POST(exchangeReq({ audience: "paladin-admin" }));

    expect(res.status).toBe(200);
    expect(h.exchangeAudience).toHaveBeenCalledTimes(1);
    expect(h.exchangeAudience).toHaveBeenCalledWith({
      refreshToken: "rt-possibly-rotated",
      targetAudience: "paladin-admin",
    });
  });

  it("401s when ExchangeAudience fails with a non-Unauthenticated error", async () => {
    h.readSessionCookie.mockResolvedValue("rt");
    h.exchangeAudience.mockImplementation(() => {
      throw { code: 13, message: "internal" };
    });
    const res = await POST(exchangeReq({ audience: "paladin-admin" }));
    expect(res.status).toBe(401);
  });
});
