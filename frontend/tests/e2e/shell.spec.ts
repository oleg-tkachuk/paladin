/**
 * The console shell's own state: version, health, background operations.
 *
 * The shell reports each section separately, so one failing badge greys
 * instead of failing the page. That is the right design and it is also the
 * most convincing way to hide a permanent break: a section that has NEVER
 * worked renders exactly like a service that is briefly down. So this asserts
 * each call came back ok.
 *
 * A unit test cannot close it. It mocks the RPCs — so a method that was
 * renamed, a plane the session role does not route, or an answer the registry
 * cannot decode all stay invisible. Only a real stack answers those.
 */
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";

// The calls the shell makes: three RPCs through the session role (ADR-0048)
// and the console's own banner.
const SHELL_CALLS = [
  "/session/rpc/paladin.iam.v1.HealthService/GetVersion",
  "/session/rpc/paladin.iam.v1.HealthService/GetHealth",
  "/session/rpc/paladin.admin.v1.PlatformOperationService/ListOperations",
  "/api/banner",
];
const HTTP_OK = 200;
const HTTP_UNAUTHORIZED = 401;

test.describe("Console shell", () => {
  test("never answers a call without the session", async ({ page }) => {
    await loginAsAdmin(page);

    // From inside the page, without the cookie: the session role is what
    // stands between the browser and the planes.
    const status = await page.evaluate(async (path) => {
      const res = await fetch(path, {
        method: "POST",
        credentials: "omit",
        headers: { "Content-Type": "application/json" },
        body: "{}",
      });
      return res.status;
    }, SHELL_CALLS[0]);
    expect(status).toBe(HTTP_UNAUTHORIZED);
  });

  test("renders version, health and background operations", async ({
    page,
  }) => {
    const answered = new Map<string, number>();
    page.on("response", (r) => {
      const path = new URL(r.url()).pathname;
      if (SHELL_CALLS.includes(path)) answered.set(path, r.status());
    });

    await loginAsAdmin(page);
    await expect
      .poll(() => answered.size, { timeout: 20_000 })
      .toBe(SHELL_CALLS.length);
    for (const path of SHELL_CALLS) {
      expect(answered.get(path), `${path} did not answer ok`).toBe(HTTP_OK);
    }
  });
});
