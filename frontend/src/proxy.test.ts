import { NextRequest } from "next/server";
import { afterEach, describe, expect, it, vi } from "vitest";

import { AUDIENCES } from "@/constants";
import { refreshCookieName } from "@/lib/auth/cookies";
import { CSP_HEADER } from "@/lib/security/csp";
import { STORAGE_ORIGINS_ENV } from "@/lib/security/storageOrigins";

const ORIGIN = "https://console.test";
const STORAGE = "https://s3.example.com";

/** The proxy as a server started with this storage list would run it. */
async function proxyWith(storage: string | undefined) {
  vi.resetModules();
  vi.stubEnv(STORAGE_ORIGINS_ENV, storage ?? "");
  return (await import("./proxy")).proxy;
}

const request = (path: string, init?: { method?: string; origin?: string }) => {
  const headers = new Headers();
  if (init?.origin) headers.set("origin", init.origin);
  return new NextRequest(`${ORIGIN}${path}`, {
    method: init?.method ?? "GET",
    headers,
  });
};

afterEach(() => {
  vi.unstubAllEnvs();
});

// The CSP moved here from next.config.ts, whose headers are written into the
// build: the storage origins presigned URLs point at are the deployment's.
describe("proxy CSP", () => {
  it("lets the browser reach the storage origins the server was started with", async () => {
    const proxy = await proxyWith(`${STORAGE}, not-an-origin`);
    const csp = proxy(request("/login")).headers.get(CSP_HEADER) ?? "";
    expect(csp).toContain(`connect-src 'self' ${STORAGE};`);
    expect(csp).toContain(`img-src 'self' data: blob: ${STORAGE};`);
    expect(csp).not.toContain("not-an-origin");
  });

  it("stays on this origin when none is named", async () => {
    const proxy = await proxyWith(undefined);
    const csp = proxy(request("/login")).headers.get(CSP_HEADER) ?? "";
    expect(csp).toContain("connect-src 'self';");
  });

  it.each([
    ["a page passed through", () => request("/login")],
    ["a redirect to the login", () => request("/tenants")],
    [
      "a refused cross-origin write",
      () =>
        request("/api/rpc/x", { method: "POST", origin: "https://evil.test" }),
    ],
  ])("covers %s", async (_, make) => {
    const proxy = await proxyWith(STORAGE);
    expect(proxy(make()).headers.get(CSP_HEADER)).toContain(
      "default-src 'self'",
    );
  });

  it("drops form-action on the consent page only", async () => {
    const proxy = await proxyWith(undefined);
    expect(
      proxy(request("/oauth/consent")).headers.get(CSP_HEADER),
    ).not.toContain("form-action");
    const signedIn = request("/tenants");
    signedIn.cookies.set(refreshCookieName(AUDIENCES.iam), "token");
    expect(proxy(signedIn).headers.get(CSP_HEADER)).toContain(
      "form-action 'self'",
    );
  });
});
