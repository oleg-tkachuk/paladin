import { afterEach, describe, expect, it, vi } from "vitest";

import { SESSION_PATHS } from "@/constants";

import {
  SessionError,
  refreshSession,
  signIn,
  signOut,
  switchSessionTenant,
} from "./client";

const answer = (status: number, body?: unknown) =>
  vi.fn(
    async () =>
      new Response(body === undefined ? null : JSON.stringify(body), {
        status,
      }),
  );

afterEach(() => vi.unstubAllGlobals());

describe("session client", () => {
  it("signs in with the credentials and nothing else", async () => {
    const fetch = answer(200, { user: {} });
    vi.stubGlobal("fetch", fetch);
    await signIn("ada", "pw");
    const [path, init] = fetch.mock.calls[0] as unknown as [
      string,
      RequestInit,
    ];
    expect(path).toBe(SESSION_PATHS.login);
    expect(init.method).toBe("POST");
    expect(init.credentials).toBe("same-origin");
    expect(JSON.parse(String(init.body))).toEqual({
      subject: "ada",
      password: "pw",
    });
  });

  it("surfaces the role's refusal as a SessionError", async () => {
    vi.stubGlobal(
      "fetch",
      answer(401, { code: "unauthenticated", message: "invalid credentials" }),
    );
    const err = await signIn("ada", "wrong").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(SessionError);
    expect(err).toMatchObject({
      message: "invalid credentials",
      code: "unauthenticated",
      status: 401,
    });
  });

  it("reads a session from 204 and none from 401 on refresh", async () => {
    const fetch = answer(204);
    vi.stubGlobal("fetch", fetch);
    await expect(refreshSession()).resolves.toEqual({ kind: "signed-in" });
    const [path, init] = fetch.mock.calls[0] as unknown as [
      string,
      RequestInit,
    ];
    expect(path).toBe(SESSION_PATHS.refresh);
    expect(init).toMatchObject({ method: "POST", credentials: "same-origin" });
    vi.stubGlobal(
      "fetch",
      answer(401, { code: "unauthenticated", message: "no session" }),
    );
    await expect(refreshSession()).resolves.toEqual({ kind: "signed-out" });
  });

  // A role that cannot check the session says nothing of it: the page must
  // not read that as signed out.
  it("reads a 503 and a failed fetch on refresh as unavailable, not signed out", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({ code: "unavailable", message: "down" }),
            {
              status: 503,
              headers: { "Retry-After": "4" },
            },
          ),
      ),
    );
    await expect(refreshSession()).resolves.toEqual({
      kind: "unavailable",
      retryAfterMs: 4_000,
    });
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new TypeError("Failed to fetch");
      }),
    );
    await expect(refreshSession()).resolves.toEqual({
      kind: "unavailable",
      retryAfterMs: null,
    });
  });

  it("switches tenant and signs out at their own paths", async () => {
    const fetch = answer(204);
    vi.stubGlobal("fetch", fetch);
    await switchSessionTenant("t-2");
    await signOut();
    const calls = fetch.mock.calls as unknown as [string, RequestInit][];
    expect(calls.map(([p]) => p)).toEqual([
      SESSION_PATHS.switchTenant,
      SESSION_PATHS.logout,
    ]);
    expect(JSON.parse(String(calls[0][1].body))).toEqual({
      targetTenantId: "t-2",
    });
  });
});
