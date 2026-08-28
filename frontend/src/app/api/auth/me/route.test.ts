import { Code, ConnectError } from "@connectrpc/connect";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const h = vi.hoisted(() => ({
  whoAmI: vi.fn(),
  exchangeAudience: vi.fn(),
  readSessionCookie: vi.fn(),
  refreshIamChain: vi.fn(),
}));

vi.mock("@/lib/auth/bff", async (orig) => {
  const actual = await orig<typeof import("@/lib/auth/bff")>();
  return {
    ...actual,
    iamAuthClient: () => ({
      whoAmI: h.whoAmI,
      exchangeAudience: h.exchangeAudience,
    }),
    readSessionCookie: h.readSessionCookie,
    refreshIamChain: h.refreshIamChain,
  };
});

import { GET } from "./route";
import { SESSION_COOKIE_NAME } from "@/lib/auth/bff";

const ROTATED = {
  accessToken: "iam-access",
  refreshToken: "iam-refresh-rotated",
  accessExpiresInSeconds: 900,
  refreshExpiresInSeconds: 604800,
};

const WHOAMI = {
  user: {
    userId: "u1",
    tenantId: "t1",
    subject: "admin",
    displayName: "Admin",
    roles: ["platform.admin"],
    resourceVersion: "v1",
  },
  tenantSlug: "platform",
};

describe("GET /api/auth/me", () => {
  beforeEach(() => {
    h.whoAmI.mockReset();
    h.exchangeAudience.mockReset();
    h.readSessionCookie.mockReset();
    h.refreshIamChain.mockReset();
    vi.spyOn(console, "error").mockImplementation(() => {});
  });
  afterEach(() => vi.restoreAllMocks());

  it("rehydrates the user (with tenantSlug) and rotates the cookie", async () => {
    h.readSessionCookie.mockResolvedValue("rt-old");
    h.refreshIamChain.mockResolvedValue(ROTATED);
    h.whoAmI.mockResolvedValue(WHOAMI);
    const res = await GET();
    expect(res.status).toBe(200);
    const json = await res.json();
    expect(json.user.subject).toBe("admin");
    expect(json.user.tenantSlug).toBe("platform");
    expect(json.accessTokens[0].audience).toBe("paladin-iam");
    expect(json.accessTokens[0].token).toBe("iam-access");
    // refreshIamChain rotates the chain → cookie carries the new refresh token.
    expect(res.cookies.get(SESSION_COOKIE_NAME)?.value).toBe(
      "iam-refresh-rotated",
    );
  });

  it("401s with no session cookie and never refreshes", async () => {
    h.readSessionCookie.mockResolvedValue(null);
    const res = await GET();
    expect(res.status).toBe(401);
    expect(h.refreshIamChain).not.toHaveBeenCalled();
  });

  it("502s when WhoAmI returns no user", async () => {
    h.readSessionCookie.mockResolvedValue("rt-old");
    h.refreshIamChain.mockResolvedValue(ROTATED);
    h.whoAmI.mockResolvedValue({ tenantSlug: "platform" });
    const res = await GET();
    expect(res.status).toBe(502);
  });

  it("401s when the refresh chain is rejected", async () => {
    h.readSessionCookie.mockResolvedValue("rt-old");
    h.refreshIamChain.mockRejectedValue(new Error("refresh token rejected"));
    const res = await GET();
    expect(res.status).toBe(401);
  });

  // ─── losing the rotation race ────────────────────────────────────────────
  //
  // Two tabs opened together each call this route with the same cookie; the
  // browser cannot update it between two requests already in flight. Exactly
  // one rotation can succeed, and the server says so with Aborted rather than
  // Unauthenticated — the session is fine, this request simply lost.
  //
  // The console used to survive that with an in-process dedup map, which is
  // what pinned it to one replica. Now the loser asks for the only thing it
  // needed: an iam access token, derived without touching the chain.

  const aborted = () =>
    new ConnectError("rotated by a concurrent request", Code.Aborted);

  it("derives an access token when a sibling rotated first", async () => {
    h.readSessionCookie.mockResolvedValue("rt-old");
    h.refreshIamChain.mockRejectedValue(aborted());
    h.exchangeAudience.mockResolvedValue({
      accessToken: "iam-access-derived",
      accessExpiresInSeconds: 900,
    });
    h.whoAmI.mockResolvedValue(WHOAMI);

    const res = await GET();
    expect(res.status).toBe(200);
    const json = await res.json();
    expect(json.user.subject).toBe("admin");
    expect(json.accessTokens[0].token).toBe("iam-access-derived");
    expect(h.exchangeAudience).toHaveBeenCalledWith({
      refreshToken: "rt-old",
      targetAudience: "paladin-iam",
    });
  });

  it("writes no cookie when it lost the race", async () => {
    // The winner is writing the real successor. Re-writing the token we still
    // hold would undo it, depending on which response the browser applied
    // last — and we have no successor of our own to write.
    h.readSessionCookie.mockResolvedValue("rt-old");
    h.refreshIamChain.mockRejectedValue(aborted());
    h.exchangeAudience.mockResolvedValue({
      accessToken: "iam-access-derived",
      accessExpiresInSeconds: 900,
    });
    h.whoAmI.mockResolvedValue(WHOAMI);

    const res = await GET();
    expect(res.cookies.get(SESSION_COOKIE_NAME)).toBeUndefined();
  });

  it("still 401s when the token is genuinely rejected", async () => {
    // The property the race handling must not cost. Unauthenticated is not
    // Aborted, so an expired or revoked token must not be quietly exchanged
    // into a working session.
    h.readSessionCookie.mockResolvedValue("rt-old");
    h.refreshIamChain.mockRejectedValue(
      new ConnectError("refresh token rejected", Code.Unauthenticated),
    );

    const res = await GET();
    expect(res.status).toBe(401);
    expect(h.exchangeAudience).not.toHaveBeenCalled();
  });

  it("401s when the fallback exchange is also refused", async () => {
    // Losing the race and then being refused an access token means the
    // supersession grace has closed: the token really is spent.
    h.readSessionCookie.mockResolvedValue("rt-old");
    h.refreshIamChain.mockRejectedValue(aborted());
    h.exchangeAudience.mockRejectedValue(
      new ConnectError("refresh token rejected", Code.Unauthenticated),
    );

    const res = await GET();
    expect(res.status).toBe(401);
  });
});
