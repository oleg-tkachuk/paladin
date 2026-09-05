/**
 * The command palette (Cmd/Ctrl+K) — a global search that was not global.
 *
 * It sent a disjunction for tenants and no filter at all for buckets,
 * collections and backends, then narrowed the four results in the browser.
 * Neither shape narrows in SQL, so every query read ONE page of each resource
 * and searched that: a tenant sorting past the ceiling could not be found by
 * typing its name, and nothing on screen said the list was cut.
 *
 * No e2e test had ever opened the palette. That is why it stayed that way.
 *
 * The unit test asserts what the palette SENDS. This asserts what comes back —
 * a filter the plane rejects and a `search` field whose Go and SQL halves
 * disagree both look identical from the sending side: a palette that finds
 * nothing.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";

const rpc = (proc: string) => (r: { url(): string }) =>
  r.url().includes("/api/rpc/") && r.url().includes(proc);

async function openPalette(page: import("@playwright/test").Page) {
  await page.keyboard.press("ControlOrMeta+k");
  const input = page.getByPlaceholder(/type to search objects/i);
  await expect(input).toBeVisible({ timeout: 10_000 });
  return input;
}

test.describe("Command palette", () => {
  test("finds a tenant by name, with the query sent as a CEL filter", async ({
    page,
    makeTenant,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();
    await gotoSettled(page, "/tenants");

    const input = await openPalette(page);
    const request = page.waitForRequest(rpc("ListTenants"), {
      timeout: 15_000,
    });
    await input.fill(tenant.slug);

    const body = JSON.parse((await request).postData() ?? "{}");
    // One conjunct over the derived field. A disjunction pushes nothing into
    // SQL, which is what made this a first-page search.
    expect(body.filter).toBe(`search.contains("${tenant.slug.toLowerCase()}")`);
    expect(body.filter).not.toContain("||");

    await expect(
      page.getByText(tenant.displayName, { exact: false }).first(),
    ).toBeVisible({ timeout: 15_000 });
  });

  // The reason `tenant_id` is in the derived field at all: an operator pastes a
  // UUID out of a log line. The palette used to match it in the browser, so
  // moving the search to the server would have dropped the case silently — and
  // this only passes if the id is in BOTH halves of the definition, the Go
  // cel.SearchText and the SQL in ListTenants.
  test("finds a tenant by the UUID pasted from a log line", async ({
    page,
    makeTenant,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();
    await gotoSettled(page, "/tenants");

    const input = await openPalette(page);
    await input.fill(tenant.tenantId);

    await expect(
      page.getByText(tenant.displayName, { exact: false }).first(),
    ).toBeVisible({ timeout: 15_000 });
  });
});
