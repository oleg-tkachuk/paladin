import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const h = vi.hoisted(() => ({
  revoke: vi.fn(),
  exchangeAudience: vi.fn(),
  readSessionCookie: vi.fn(),
}));

vi.mock("@/lib/auth/bff", async (orig) => {
  const actual = await orig<typeof import("@/lib/auth/bff")>();
  return {
    ...actual,
    iamAuthClient: () => ({
      revoke: h.revoke,
      exchangeAudience: h.exchangeAudience,
    }),
    readSessionCookie: h.readSessionCookie,
  };
});

import { POST } from "./route";
import { SESSION_COOKIE_NAME } from "@/lib/auth/bff";

// The previous version of this file mocked revoke as resolving and asserted
// it was called with { token }. Both were true, and logout still revoked
// nothing: the IAM plane requires an Authorization header on Revoke, the
// route sent none, and the resulting error was swallowed by a catch. The
// user appeared logged out while the refresh chain stayed live for a week.
//
// So the assertions that matter here are the bearer and the reported outcome.

describe("POST /api/auth/logout", () => {
  beforeEach(() => {
    h.revoke.mockReset();
    h.exchangeAudience.mockReset();
    h.readSessionCookie.mockReset();
    vi.spyOn(console, "error").mockImplementation(() => {});
  });
  afterEach(() => vi.restoreAllMocks());

  it("revokes the chain with a bearer and clears the cookie", async () => {
    h.readSessionCookie.mockResolvedValue("rt-token");
    h.exchangeAudience.mockResolvedValue({
      accessToken: "iam-access",
      accessExpiresInSeconds: 900,
    });
    h.revoke.mockResolvedValue({});

    const res = await POST();
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ ok: true, revoked: true });
    // Without this header the server refuses the call outright.
    expect(h.revoke).toHaveBeenCalledWith(
      { token: "rt-token" },
      { headers: { Authorization: "Bearer iam-access" } },
    );
    const cookie = res.cookies.get(SESSION_COOKIE_NAME);
    expect(cookie?.value).toBe("");
    expect(cookie?.maxAge).toBe(0);
  });

  it("derives the bearer without rotating the chain", async () => {
    // ExchangeAudience, not RefreshToken: this route is about to revoke the
    // chain, so rotating it first would leave a fresh token behind if the
    // revoke then failed.
    h.readSessionCookie.mockResolvedValue("rt-token");
    h.exchangeAudience.mockResolvedValue({
      accessToken: "iam-access",
      accessExpiresInSeconds: 900,
    });
    h.revoke.mockResolvedValue({});

    await POST();
    expect(h.exchangeAudience).toHaveBeenCalledWith({
      refreshToken: "rt-token",
      targetAudience: "paladin-iam",
    });
  });

  it("clears the cookie but reports revoked:false when IAM refuses", async () => {
    // The local logout must still happen — but saying ok:true alone would
    // claim a session was ended when it was not.
    h.readSessionCookie.mockResolvedValue("rt-token");
    h.exchangeAudience.mockResolvedValue({
      accessToken: "iam-access",
      accessExpiresInSeconds: 900,
    });
    h.revoke.mockRejectedValue(new Error("revoke down"));

    const res = await POST();
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ ok: true, revoked: false });
    expect(res.cookies.get(SESSION_COOKIE_NAME)?.maxAge).toBe(0);
  });

  it("reports revoked:false when the bearer cannot be derived", async () => {
    h.readSessionCookie.mockResolvedValue("rt-token");
    h.exchangeAudience.mockRejectedValue(new Error("token expired"));

    const res = await POST();
    expect(await res.json()).toEqual({ ok: true, revoked: false });
    expect(h.revoke).not.toHaveBeenCalled();
    expect(res.cookies.get(SESSION_COOKIE_NAME)?.maxAge).toBe(0);
  });

  it("does not call revoke when there is no session cookie", async () => {
    h.readSessionCookie.mockResolvedValue(null);
    const res = await POST();
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ ok: true, revoked: false });
    expect(h.revoke).not.toHaveBeenCalled();
    expect(res.cookies.get(SESSION_COOKIE_NAME)?.maxAge).toBe(0);
  });
});
