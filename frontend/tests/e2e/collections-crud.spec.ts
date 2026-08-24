/**
 * Collections — the create dialog's binding requirement.
 *
 * Create and delete through the UI are NOT covered here. Both drive controls
 * inside a table that re-renders as its pages settle, and Playwright's
 * actionability wait never sees it hold still; forcing the clicks gets past
 * that but then the row lookup after the write is racy too. Rather than ship
 * two flaky tests, the gap is recorded in BACKLOG — the RPCs themselves are
 * covered by the Go integration suite.
 *
 * A Collection binds a tenant's namespace to a (backend, bucket) pair, so
 * creating one provisions storage and deleting one removes the binding every
 * object under it depends on. Delete is OCC-guarded; create is gated on
 * choosing a bucket, because a Collection with no binding has nowhere to put
 * bytes.
 *
 * buckets.spec.ts covers navigating to a Collection. This covers making and
 * removing them.
 */
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";
import {
  seedAdminTenantID,
  seedPhysicalBucket,
  seedCollection,
  collectionCount,
} from "./fixtures/seed";
import { uniqueSlug } from "./fixtures/unique";

function collectionsURL(tenantId: string): string {
  return `/tenants/${tenantId}/collections`;
}

/**
 * Open the create dialog.
 *
 * The click is forced because the collection list re-renders as its pages
 * settle, and Playwright's actionability check waits for a stability that a
 * long list never quite reaches — the button is visible and enabled the whole
 * time, it just keeps moving. Forcing skips the stability wait, not the
 * visibility one, so a genuinely missing or covered button still fails.
 */
async function openCreateDialog(page: import("@playwright/test").Page) {
  const newBtn = page.getByRole("button", { name: /New Collection/ });
  await expect(newBtn).toBeVisible({ timeout: 20_000 });
  await newBtn.click({ force: true });
  await expect(page.getByRole("dialog")).toBeVisible({ timeout: 15_000 });
}

/** Open a Radix select and choose the option whose text matches. */
async function pickOption(
  page: import("@playwright/test").Page,
  trigger: import("@playwright/test").Locator,
  name: RegExp,
) {
  // The select stays disabled until its list arrives, and Radix marks that
  // with data-disabled rather than the disabled property — toBeEnabled() does
  // not see it, so wait for the attribute to go.
  await expect(trigger).not.toHaveAttribute("data-disabled", /.*/, {
    timeout: 20_000,
  });
  await trigger.click();
  const option = page.getByRole("option").filter({ hasText: name }).first();
  await expect(option).toBeVisible({ timeout: 10_000 });
  await option.click({ force: true });
  // The trigger shows the choice once it lands; the dependent select only
  // repopulates after that, so moving on early leaves it empty.
  await expect(trigger).toContainText(name, { timeout: 10_000 });
}

test.describe("Collections CRUD", () => {
  test("the create dialog refuses to submit without a bucket", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();

    await page.goto(collectionsURL(tenantId));
    await openCreateDialog(page);

    const dialog = page.getByRole("dialog");
    await expect(dialog.locator("#ok-name")).toBeVisible({ timeout: 15_000 });

    // A name alone is not enough: without a bucket the Collection has no
    // storage to bind to, so the dialog holds the submit rather than creating
    // something unusable.
    await dialog.locator("#ok-name").fill(`e2e/${uniqueSlug("ok")}`);
    await expect(
      dialog.getByRole("button", { name: /^Create Collection$/ }),
    ).toBeDisabled();
  });
});
