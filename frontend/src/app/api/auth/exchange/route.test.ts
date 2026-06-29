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

  it("iam audience rotates the chain via RefreshToken + updates the cookie", async () => {
    h.readSessionCookie.mockResolvedValue("rt-old");
    h.refreshIamChain.mockResolvedValue({
      accessToken: "iam-access",
      refreshToken: "iam-refresh-new",
      accessExpiresInSeconds: 900,
      refreshExpiresInSeconds: 604800,
    });
    const res = await POST(exchangeReq({ audience: "paladin-iam" }));
    expect(res.status).toBe(200);
    const json = await res.json();
    expect(json.audience).toBe("paladin-iam");
    expect(json.token).toBe("iam-access");
    expect(h.exchangeAudience).not.toHaveBeenCalled();
    expect(res.cookies.get(SESSION_COOKIE_NAME)?.value).toBe("iam-refresh-new");
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

  it("self-heals an Unauthenticated ExchangeAudience by re-reading the rotated cookie and retrying once", async () => {
    // First read returns the stale token; a parallel iam refresh has rotated
    // the cookie, so the retry re-reads a fresh one.
    h.readSessionCookie
      .mockResolvedValueOnce("rt-stale")
      .mockResolvedValueOnce("rt-fresh");
    h.exchangeAudience
      .mockImplementationOnce(() => {
        throw { code: 16 }; // Connect Code.Unauthenticated
      })
      .mockResolvedValueOnce({
        accessToken: "data-access",
        accessExpiresInSeconds: 300,
      });
    const res = await POST(exchangeReq({ audience: "paladin-admin" }));
    expect(res.status).toBe(200);
    expect((await res.json()).token).toBe("data-access");
    expect(h.exchangeAudience).toHaveBeenCalledTimes(2);
    expect(h.exchangeAudience).toHaveBeenLastCalledWith({
      refreshToken: "rt-fresh",
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
