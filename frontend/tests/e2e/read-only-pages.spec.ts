/**
 * The read-only surfaces: audit, health, MCP, stats, config, billing.
 *
 * These pages mutate nothing, so what is worth asserting is not what they let
 * you do — it is that every RPC behind them actually answers. That is exactly
 * how ListOperations went unnoticed: the background-operations drawer returned
 * 500 on every call carrying an operation, and nothing in the suite looked at
 * a page long enough to notice.
 *
 * So each case opens a page, waits for it to settle, and fails on any 5xx or
 * any Connect error frame the console logged. A page that renders its heading
 * while its data call is failing looks fine to a screenshot and is broken to a
 * user.
 */
import { test, expect, type Page } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";

/** Records RPC failures the page produces, from both directions: the HTTP
 *  status and the client's own error log — a Connect error can arrive inside
 *  a 200 in some streaming shapes. */
function watchRPC(page: Page) {
  const failures: string[] = [];
  const calls: string[] = [];
  page.on("response", (r) => {
    if (!/\/api\/rpc\//.test(r.url())) return;
    calls.push(r.url().split("/rpc/")[1]);
    if (r.status() >= 400) {
      failures.push(`${r.status()} ${r.url().split("/rpc/")[1]}`);
    }
  });
  page.on("console", (m) => {
    const t = m.text();
    if (/\[RPC Error\]/.test(t)) failures.push(t.slice(0, 200));
  });
  return { failures, calls };
}

/**
 * `content` is a string the page can only render once its data arrived — a
 * column header from a populated table, a section that is conditional on a
 * response. Asserting on it separates "the shell rendered" from "the data
 * landed", which is the difference a heading check cannot see.
 */
const PAGES = [
  { path: "/audit", heading: /Audit Logs/i, content: /Action/ },
  { path: "/health", heading: /Health & Diagnostics/i, content: /postgres/i },
  { path: "/mcp", heading: /MCP server/i, content: /Connecting an agent/i },
  { path: "/stats", heading: /Platform Statistics/i, content: /By backend/i },
  { path: "/config", heading: /Configuration/i, content: /Cluster snapshot/i },
  { path: "/billing", heading: /Billing/i, content: /Spend over time/i },
] as const;

test.describe("Read-only pages", () => {
  for (const { path, heading, content } of PAGES) {
    test(`${path} renders and every RPC behind it answers`, async ({
      page,
    }) => {
      await loginAsAdmin(page);
      const { failures, calls } = watchRPC(page);

      await page.goto(path);
      await expect(
        page.getByRole("heading", { level: 1, name: heading }),
      ).toBeVisible({ timeout: 20_000 });

      // Something only a loaded page can show. Without this the test passes on
      // a page whose shell rendered and whose data call quietly failed.
      await expect(page.getByText(content).first()).toBeVisible({
        timeout: 20_000,
      });

      // Let the deferred calls land — several of these pages fetch on mount
      // and again once a scope resolves.
      await page.waitForTimeout(6_000);

      // A page that fetched nothing would pass the check above trivially, and
      // this suite exists precisely to notice a data call that stopped
      // working. Requiring at least one RPC keeps the assertion honest.
      expect(calls.length, `${path} made no RPC calls at all`).toBeGreaterThan(
        0,
      );
      expect(
        failures,
        `${path} produced RPC failures:\n${failures.join("\n")}`,
      ).toEqual([]);
    });
  }

  test("/ops redirects to /health rather than 404ing", async ({ page }) => {
    await loginAsAdmin(page);

    // /ops is a redirect stub. It exists because the nav and older links point
    // at it; if the redirect broke, the only symptom would be a dead menu item.
    await page.goto("/ops");
    await expect(page).toHaveURL(/\/health$/, { timeout: 15_000 });
    await expect(
      page.getByRole("heading", { level: 1, name: /Health & Diagnostics/i }),
    ).toBeVisible({ timeout: 15_000 });
  });
});
