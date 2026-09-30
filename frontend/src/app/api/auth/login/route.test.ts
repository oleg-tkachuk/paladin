import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { Code, ConnectError } from "@connectrpc/connect";

const h = vi.hoisted(() => ({ login: vi.fn() }));

vi.mock("@/lib/auth/bff", async (orig) => {
  const actual = await orig<typeof import("@/lib/auth/bff")>();
  return {
    ...actual,
    iamAuthClient: () => ({ login: h.login }),
  };
});

import { POST } from "./route";
import {
  INVALID_CREDENTIALS_MESSAGE,
  SIGN_IN_UNAVAILABLE_MESSAGE,
} from "@/lib/auth/loginFailure";
import { SESSION_COOKIE_NAME } from "@/lib/auth/bff";

function loginReq(body: unknown) {
  return new Request("https://app.test/api/auth/login", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: typeof body === "string" ? body : JSON.stringify(body),
  });
}

const OK_LOGIN = {
  tokens: {
    accessToken: "iam-access",
    refreshToken: "iam-refresh",
    accessExpiresInSeconds: 900,
    refreshExpiresInSeconds: 604800,
  },
  user: {
    userId: "u1",
    tenantId: "t1",
    subject: "admin",
    displayName: "Admin",
    roles: ["platform.admin"],
    resourceVersion: "v1",
  },
};

describe("POST /api/auth/login", () => {
  beforeEach(() => {
    h.login.mockReset();
    // Routes console.error their caught failures; silence so vitest doesn't
    // surface the expected error log as an unhandled error.
    vi.spyOn(console, "error").mockImplementation(() => {});
  });
  afterEach(() => vi.restoreAllMocks());

  it("returns the user + iam access token and sets the refresh cookie", async () => {
    h.login.mockResolvedValue(OK_LOGIN);
    const res = await POST(loginReq({ subject: "admin", password: "pw" }));
    expect(res.status).toBe(200);
    const json = await res.json();
    expect(json.user.subject).toBe("admin");
    expect(json.accessTokens).toHaveLength(1);
    expect(json.accessTokens[0].audience).toBe("paladin-iam");
    expect(json.accessTokens[0].token).toBe("iam-access");
    const cookie = res.cookies.get(SESSION_COOKIE_NAME);
    expect(cookie?.value).toBe("iam-refresh");
    expect(cookie?.httpOnly).toBe(true);
  });

  it("400s on missing credentials without calling IAM", async () => {
    const res = await POST(loginReq({ subject: "admin" }));
    expect(res.status).toBe(400);
    expect(h.login).not.toHaveBeenCalled();
  });

  it("400s on invalid JSON", async () => {
    const res = await POST(loginReq("{not json"));
    expect(res.status).toBe(400);
  });

  it("401s when IAM rejects the credentials, in words for the form", async () => {
    h.login.mockRejectedValue(
      new ConnectError("invalid credentials", Code.Unauthenticated),
    );
    const res = await POST(loginReq({ subject: "admin", password: "bad" }));
    expect(res.status).toBe(401);
    expect((await res.json()).error).toBe(INVALID_CREDENTIALS_MESSAGE);
    expect(res.cookies.get(SESSION_COOKIE_NAME)?.value).toBeFalsy();
  });

  it("403s with IAM's reason when the account may not sign in", async () => {
    h.login.mockRejectedValue(
      new ConnectError("user is disabled", Code.PermissionDenied),
    );
    const res = await POST(loginReq({ subject: "admin", password: "pw" }));
    expect(res.status).toBe(403);
    expect((await res.json()).error).toBe("user is disabled");
  });

  // An unreachable IAM is not a wrong password.
  it("502s when IAM cannot be reached", async () => {
    h.login.mockRejectedValue(new TypeError("fetch failed"));
    const res = await POST(loginReq({ subject: "admin", password: "pw" }));
    expect(res.status).toBe(502);
    expect((await res.json()).error).toBe(
      `${SIGN_IN_UNAVAILABLE_MESSAGE}: fetch failed`,
    );
  });

  it("502s when IAM returns no token pair", async () => {
    h.login.mockResolvedValue({ user: OK_LOGIN.user });
    const res = await POST(loginReq({ subject: "admin", password: "pw" }));
    expect(res.status).toBe(502);
  });
});
