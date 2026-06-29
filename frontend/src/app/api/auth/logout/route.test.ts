import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const h = vi.hoisted(() => ({ revoke: vi.fn(), readSessionCookie: vi.fn() }));

vi.mock("@/lib/auth/bff", async (orig) => {
  const actual = await orig<typeof import("@/lib/auth/bff")>();
  return {
    ...actual,
    iamAuthClient: () => ({ revoke: h.revoke }),
    readSessionCookie: h.readSessionCookie,
  };
});

import { POST } from "./route";
import { SESSION_COOKIE_NAME } from "@/lib/auth/bff";

describe("POST /api/auth/logout", () => {
  beforeEach(() => {
    h.revoke.mockReset();
    h.readSessionCookie.mockReset();
    vi.spyOn(console, "warn").mockImplementation(() => {});
  });
  afterEach(() => vi.restoreAllMocks());

  it("revokes the chain and clears the cookie", async () => {
    h.readSessionCookie.mockResolvedValue("rt-token");
    h.revoke.mockResolvedValue({});
    const res = await POST();
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ ok: true });
    expect(h.revoke).toHaveBeenCalledWith({ token: "rt-token" });
    const cookie = res.cookies.get(SESSION_COOKIE_NAME);
    expect(cookie?.value).toBe("");
    expect(cookie?.maxAge).toBe(0);
  });

  it("clears the cookie even when IAM revoke fails", async () => {
    h.readSessionCookie.mockResolvedValue("rt-token");
    h.revoke.mockRejectedValue(new Error("revoke down"));
    const res = await POST();
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ ok: true });
    expect(res.cookies.get(SESSION_COOKIE_NAME)?.maxAge).toBe(0);
  });

  it("does not call revoke when there is no session cookie", async () => {
    h.readSessionCookie.mockResolvedValue(null);
    const res = await POST();
    expect(res.status).toBe(200);
    expect(h.revoke).not.toHaveBeenCalled();
    expect(res.cookies.get(SESSION_COOKIE_NAME)?.maxAge).toBe(0);
  });
});
