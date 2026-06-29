import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const h = vi.hoisted(() => ({
  whoAmI: vi.fn(),
  readSessionCookie: vi.fn(),
  refreshIamChain: vi.fn(),
}));

vi.mock("@/lib/auth/bff", async (orig) => {
  const actual = await orig<typeof import("@/lib/auth/bff")>();
  return {
    ...actual,
    iamAuthClient: () => ({ whoAmI: h.whoAmI }),
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
});
