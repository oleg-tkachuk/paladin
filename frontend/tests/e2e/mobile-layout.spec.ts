/**
 * The console at phone width.
 *
 * A row that does not wrap widens the whole document, and the page then
 * scrolls sideways: on every page when it is the top bar, on one page when it
 * is a toolbar. Nothing renders wrong at desktop width, so only a check made at
 * phone width sees it.
 */
import { test, expect } from "./fixtures/resources";
import type { Page } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";

/** The narrowest phone the console supports (iPhone SE / mini class). */
const PHONE = { width: 375, height: 812 } as const;

/** How far the document extends past the viewport, in CSS pixels. */
async function horizontalOverflow(page: Page): Promise<number> {
  return page.evaluate(
    () =>
      document.documentElement.scrollWidth -
      document.documentElement.clientWidth,
  );
}

test.describe("Phone-width layout", () => {
  test.use({ viewport: PHONE });

  test("pages do not scroll sideways", async ({
    page,
    makeTenant,
    makeBucket,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();
    // The objects page names the collection's bucket; a long one is what
    // pushed that strip past the edge.
    const bucket = await makeBucket({ ownerTenantId: tenant.tenantId });
    const collection = await makeCollection({
      tenantId: tenant.tenantId,
      bucket,
    });
    const tenantPath = `/tenants/${encodeURIComponent(tenant.slug)}`;

    for (const path of [
      "/",
      `${tenantPath}/capabilities`,
      `${tenantPath}/collections/${encodeURIComponent(collection.collection)}/objects`,
    ]) {
      await gotoSettled(page, path);
      // Soft, so one run names every page that overflows, not just the first.
      expect.soft(await horizontalOverflow(page), path).toBeLessThanOrEqual(0);
    }
  });

  // A table wider than the phone scrolled inside its box, so a row's last
  // columns were out of sight; at this width a row is a card instead.
  test("a list's rows are cards, each value named by its column", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    await gotoSettled(page, "/users");
    const table = page.getByRole("table");
    await expect(table.locator("thead")).toBeHidden();
    const firstCell = table.locator("tbody tr").first().locator("td").first();
    await expect(firstCell).toHaveAttribute("data-label", /User/);
    expect(await horizontalOverflow(page)).toBeLessThanOrEqual(0);
  });
});
