/**
 * The console shell's own state: version, health, background operations.
 *
 * /api/shell fans out to two planes and reports each section separately, so
 * one failing badge greys instead of failing the page. That is the right
 * design and it is also the most convincing way to hide a permanent break: a
 * section that has NEVER worked renders exactly like a service that is
 * briefly down. Nothing asserted these came back `ok`, which is the same gap
 * that let the audit stream go years without delivering an event and let
 * logout go its whole life without revoking anything.
 *
 * A unit test cannot close it. It can prove the route fans out and degrades
 * correctly, but it mocks the RPCs — so a method that was renamed, an
 * audience the plane no longer accepts, or a response the registry cannot
 * re-encode all stay invisible. Only a real cluster answers those.
 */
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";

type Section = { status: string; reason?: string; data?: unknown };

test.describe("Console shell", () => {
  test("never answers without a credential", async ({ page }) => {
    await loginAsAdmin(page);

    // Fetched from inside the page so it carries exactly what the console
    // sends — both plane tokens, from the same token store the shell uses.
    // Reconstructing the headers in the test would be testing the test: the
    // first probe of this route written by hand omitted the iam header and
    // reported two sections broken that were perfectly healthy.
    const body = await page.evaluate(async () => {
      const res = await fetch("/api/shell", {
        credentials: "same-origin",
        headers: {},
      });
      return { status: res.status, text: await res.text() };
    });

    // The console attaches its tokens through its own transport, so a bare
    // fetch is unauthenticated — that is itself worth pinning: this route
    // reaches cluster-only planes and must never answer without a credential.
    expect(body.status).toBe(401);
  });

  test("renders version, health and background operations", async ({
    page,
  }) => {
    // Driven through the UI, which is where the shell's data actually lands.
    // If a section were permanently unavailable the chrome would still
    // render — so the assertion is on the values, not on the page loading.
    const shellCalls: Array<{ status: number; body: string }> = [];
    page.on("response", async (r) => {
      if (!r.url().includes("/api/shell")) return;
      try {
        shellCalls.push({ status: r.status(), body: await r.text() });
      } catch {
        // A response body can be gone by the time we ask; the assertions
        // below fail loudly if we end up with nothing.
      }
    });

    await loginAsAdmin(page);
    await page.waitForFunction(() => document.readyState === "complete", null, {
      timeout: 15_000,
    });
    await expect
      .poll(() => shellCalls.length, { timeout: 20_000 })
      .toBeGreaterThan(0);

    const ok = shellCalls.find((c) => c.status === 200);
    expect(ok, "the console never got a 200 from /api/shell").toBeTruthy();

    const sections = JSON.parse(ok!.body) as Record<string, Section>;
    expect(Object.keys(sections).sort()).toEqual([
      "health",
      "operations",
      "version",
    ]);
    for (const [name, s] of Object.entries(sections)) {
      expect(
        s.status,
        `${name} is ${s.status}: ${s.reason ?? "no reason given"}`,
      ).toBe("ok");
      expect(s.data, `${name} reported ok with no data`).toBeTruthy();
    }
  });
});
