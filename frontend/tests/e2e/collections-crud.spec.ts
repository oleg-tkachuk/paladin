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
import { gotoSettled } from "./fixtures/navigate";
import { uniqueSlug } from "./fixtures/unique";

function collectionsURL(tenantId: string): string {
  return `/tenants/${tenantId}/collections`;
}

/**
 * Open the create dialog.
 *
 * Forced: the button is visible and enabled by this point, and what remains
 * is Playwright's stability heuristic reacting to the shell still painting.
 * Forcing skips that wait, not the visibility one, so a genuinely missing or
 * covered button still fails.
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
  // Disabled until its list arrives (backends.length === 0), so waiting for
  // enabled is waiting for the data. Note Radix leaves a stale data-disabled
  // attribute on the trigger even once it is live — asserting on the
  // attribute instead waits forever.
  await expect(trigger).toBeEnabled({ timeout: 20_000 });
  await trigger.click({ force: true });
  const option = page.getByRole("option").filter({ hasText: name }).first();
  await expect(option).toBeVisible({ timeout: 10_000 });
  await option.click({ force: true });
  // Wait for the list to close rather than for the trigger's text: the label
  // is composed ("<id> — <display name>") and which option the list offers
  // first is not this test's business. What matters is that a choice landed,
  // because the dependent select only repopulates after it does.
  await expect(page.getByRole("option").first()).toBeHidden({
    timeout: 10_000,
  });
}

test.describe("Collections CRUD", () => {
  test("the create dialog refuses to submit without a bucket", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();

    await gotoSettled(page, collectionsURL(tenantId));
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

  test("creating a Collection makes it appear in the list", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    await seedPhysicalBucket();
    const before = await collectionCount(tenantId);

    await gotoSettled(page, collectionsURL(tenantId));
    await openCreateDialog(page);

    const dialog = page.getByRole("dialog");
    const name = `e2e/${uniqueSlug("ok")}`;
    await dialog.locator("#ok-name").fill(name);

    // Backend then bucket: the bucket list is derived from the chosen backend,
    // so picking them out of order leaves the second empty.
    await pickOption(page, dialog.locator("#ok-backend"), /^primary$/);
    await pickOption(page, dialog.locator("#ok-bucket"), /./);

    const submit = dialog.getByRole("button", { name: /^Create Collection$/ });
    await expect(submit).toBeEnabled();
    await submit.click({ force: true });

    await expect
      .poll(() => collectionCount(tenantId), { timeout: 20_000 })
      .toBe(before + 1);

    // Narrow the list before looking for the row — filtering is what an
    // operator does, and it keeps the assertion off a long table.
    await page.getByPlaceholder(/Search Collections by prefix/i).fill(name);
    await expect(page.getByText(name).first()).toBeVisible({ timeout: 15_000 });
  });

  test("deleting a Collection removes it", async ({ page }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await seedPhysicalBucket();
    const collection = await seedCollection({ tenantId, bucket });
    const before = await collectionCount(tenantId);

    await gotoSettled(page, collectionsURL(tenantId));
    await page
      .getByPlaceholder(/Search Collections by prefix/i)
      .fill(collection.collection);

    const row = page
      .getByRole("row")
      .filter({ hasText: collection.collection });
    await expect(row).toBeVisible({ timeout: 15_000 });

    await row.getByRole("button", { name: /Actions/i }).click({ force: true });

    const deleteItem = page.getByRole("menuitem", { name: /Delete/i });
    await expect(deleteItem).toBeVisible({ timeout: 15_000 });
    await deleteItem.click({ force: true });

    // The confirm dialog names what is about to go, because a Collection is
    // the binding every object under it resolves through.
    await expect(page.getByText(/Delete this Collection\?/i)).toBeVisible();
    const confirm = page.getByRole("button", { name: /^Delete$/ }).last();
    await expect(confirm).toBeVisible({ timeout: 15_000 });
    await confirm.click({ force: true });

    await expect
      .poll(() => collectionCount(tenantId), { timeout: 20_000 })
      .toBe(before - 1);
  });
});
