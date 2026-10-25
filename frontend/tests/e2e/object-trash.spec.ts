/**
 * Object trash — the Version column and the OCC guard behind Restore.
 *
 * Restore used to accept an absent resource_version and skip the check, so a
 * restore could overwrite a concurrent change. It is mandatory now, and the
 * console reads the value from the trash listing — which is why that listing
 * displays it. Both halves are asserted here: the version is on screen, and
 * a restore driven from that screen actually succeeds.
 *
 * These run against the object trash (per-collection), not the tenant trash
 * covered by trash.spec.ts.
 */
import { type Page } from "@playwright/test";

import { test, expect, type MakeCollection } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import {
  seedAdminTenantID,
  seedObject,
  seedPhysicalBucket,
  softDeleteObject,
} from "./fixtures/seed";
import { gotoSettled } from "./fixtures/navigate";
import { NO_DURATION } from "@/lib/format/duration";
import { PAGE_TABLE, tableParam } from "@/lib/table/urlState";

/** How long a seeded row, the panel or a confirmed action takes to show. */
const SETTLE_MS = 10_000;
/** The URL parameter the trash keeps its open object under. */
const OPEN_PARAM = tableParam("row", PAGE_TABLE);
/** The panel's field for how long the object has sat in the trash. */
const IN_TRASH_FOR = "In trash for";
/** The word the purge confirmation asks to be typed. */
const PURGE_WORD = "purge";

/** Tenant + physical bucket + collection — the same scope
 *  object-lifecycle.spec.ts builds, since these tests also move real bytes. */
async function seedScope(makeCollection: MakeCollection) {
  const tenantId = await seedAdminTenantID();
  const bucket = await seedPhysicalBucket();
  const collection = await makeCollection({ tenantId, bucket });
  return { tenantId, collection: collection.collection };
}

/** Trash page for one collection. The collection name may contain slashes,
 *  so it is encoded as a single path segment. */
function trashURL(tenantId: string, collection: string): string {
  return `/tenants/${tenantId}/collections/${encodeURIComponent(collection)}/trash`;
}

/** The last segment of an object's key: what its row and panel are named. */
function baseName(key: string): string {
  return key.split("/").pop() ?? key;
}

/** `?id=<objectId>` (or `&id=`) in the address bar. */
function openParamRE(objectId: string): RegExp {
  return new RegExp(`[?&]${OPEN_PARAM}=${objectId}(&|$)`);
}

test.describe("Object trash — version + restore", () => {
  test("a soft-deleted object shows its resource version in the listing", async ({
    page,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    const { tenantId, collection } = await seedScope(makeCollection);
    const obj = await seedObject({ tenantId, collection });
    await softDeleteObject(obj.name);

    await gotoSettled(page, trashURL(tenantId, collection));

    const row = page.getByRole("row").filter({ hasText: obj.objectId });
    await expect(row).toBeVisible({ timeout: 10_000 });

    // The Version column is the OCC guard the Restore button will send. An
    // empty cell would mean the console has nothing to send and the server
    // would reject the restore — so the value being present is the contract,
    // not decoration.
    await expect(
      page.getByRole("columnheader", { name: "Version" }),
    ).toBeVisible();
    await expect(row.getByText(/^\d+$/)).toBeVisible();
  });

  test("restore returns the object to its collection and empties the trash", async ({
    page,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    const { tenantId, collection } = await seedScope(makeCollection);
    const obj = await seedObject({ tenantId, collection });
    await softDeleteObject(obj.name);

    await gotoSettled(page, trashURL(tenantId, collection));
    const row = page.getByRole("row").filter({ hasText: obj.objectId });
    await expect(row).toBeVisible({ timeout: 10_000 });

    await row.getByRole("button", { name: /Restore/ }).click();
    // Restore is confirmed first, like every delete and restore.
    await page.getByRole("button", { name: /^Restore object$/ }).click();

    // The row leaving the trash is the observable effect of a restore the
    // server accepted. If the OCC guard were missing or stale the call would
    // fail and the row would still be here.
    await expect(row).toBeHidden({ timeout: 10_000 });
    await expect(page.getByText(/Trash is empty/i)).toBeVisible({
      timeout: 10_000,
    });
  });

  test("an empty trash says so rather than rendering a bare table", async ({
    page,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    const { tenantId, collection } = await seedScope(makeCollection);

    await gotoSettled(page, trashURL(tenantId, collection));

    await expect(page.getByText(/Trash is empty/i)).toBeVisible({
      timeout: 10_000,
    });
  });
});

// A collection whose last object is in its trash cannot be deleted — the
// trashed object can still be restored — and the refusal used to say "has
// live objects", which it had none of. It now says the trash holds it, and
// the notice opens that trash.
test.describe("Object trash — holds its collection", () => {
  test("a delete the trash refuses says so and opens the trash", async ({
    page,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    const { tenantId, collection } = await seedScope(makeCollection);
    const obj = await seedObject({ tenantId, collection });
    await softDeleteObject(obj.name);

    await gotoSettled(page, `/tenants/${tenantId}/collections`);
    await page
      .getByRole("button", { name: `Actions for ${collection}` })
      .click();
    await page.getByRole("menuitem", { name: /Delete Collection/i }).click();
    await page
      .getByRole("textbox", { name: `Type ${collection} to confirm` })
      .fill(collection);
    await page.getByRole("button", { name: /^Delete Collection$/ }).click();

    await expect(page.getByText(/1 object in its trash/)).toBeVisible();
    await page.getByRole("button", { name: "Open its trash" }).click();
    // The tenant segment lands as its slug: the workspace canonicalises an id.
    await expect(page).toHaveURL(
      new RegExp(`/collections/${encodeURIComponent(collection)}/trash$`),
    );
    await expect(
      page.getByRole("row").filter({ hasText: obj.objectId }),
    ).toBeVisible({ timeout: 10_000 });
  });
});

// The trashed object's record opens in the detail panel beside the list, its
// id in the URL, with Restore and Purge along the bottom. The unit tests
// render the panel alone; these drive it on the page, against the server.
test.describe("Object trash — detail panel", () => {
  /** A soft-deleted object, and the trash page listing it. */
  async function trashedObject(page: Page, makeCollection: MakeCollection) {
    await loginAsAdmin(page);
    const { tenantId, collection } = await seedScope(makeCollection);
    const obj = await seedObject({ tenantId, collection });
    await softDeleteObject(obj.name);
    return { tenantId, collection, obj };
  }

  /** The panel for one object: a dialog named after its key. */
  function panelFor(page: Page, obj: { key: string }) {
    return page.getByRole("dialog", { name: baseName(obj.key) });
  }

  test("a clicked row opens its panel, with the id in the URL", async ({
    page,
    makeCollection,
  }) => {
    const { tenantId, collection, obj } = await trashedObject(
      page,
      makeCollection,
    );
    await gotoSettled(page, trashURL(tenantId, collection));

    const row = page.getByRole("row").filter({ hasText: obj.objectId });
    await expect(row).toBeVisible({ timeout: SETTLE_MS });
    await row.getByText(baseName(obj.key), { exact: true }).click();

    const panel = panelFor(page, obj);
    await expect(panel).toBeVisible({ timeout: SETTLE_MS });
    await expect(panel).toContainText(obj.objectId);
    await expect(page).toHaveURL(openParamRE(obj.objectId));

    // How long it has been in the trash is measured from the deletion: a
    // missing deletion time, or one the page could not measure, reads as the
    // empty duration. The exact span depends on the clock, so only that it
    // was measured is asserted.
    const field = panel.getByText(IN_TRASH_FOR, { exact: true }).locator("..");
    await expect(field).toBeVisible();
    await expect(field).not.toContainText(NO_DURATION);
    await expect(field).toHaveText(new RegExp(`^${IN_TRASH_FOR}.+`));
  });

  test("a link with the object's id opens its panel on load", async ({
    page,
    makeCollection,
  }) => {
    const { tenantId, collection, obj } = await trashedObject(
      page,
      makeCollection,
    );
    await gotoSettled(
      page,
      `${trashURL(tenantId, collection)}?${OPEN_PARAM}=${obj.objectId}`,
    );

    // The workspace canonicalises the tenant segment; the open object has to
    // survive that, or the link opens a bare list.
    await expect(page).toHaveURL(openParamRE(obj.objectId));
    const panel = panelFor(page, obj);
    await expect(panel).toBeVisible({ timeout: SETTLE_MS });
    await expect(panel).toContainText(obj.objectId);
    await expect(panel.getByText(IN_TRASH_FOR, { exact: true })).toBeVisible();

    // And a reload of that address opens it again.
    await page.reload();
    await expect(panelFor(page, obj)).toBeVisible({ timeout: SETTLE_MS });
  });

  test("restore from the panel is confirmed, then empties the trash", async ({
    page,
    makeCollection,
  }) => {
    const { tenantId, collection, obj } = await trashedObject(
      page,
      makeCollection,
    );
    await gotoSettled(
      page,
      `${trashURL(tenantId, collection)}?${OPEN_PARAM}=${obj.objectId}`,
    );
    const panel = panelFor(page, obj);
    await expect(panel).toBeVisible({ timeout: SETTLE_MS });

    await panel.getByRole("button", { name: /^Restore$/ }).click();
    const confirm = page.getByRole("alertdialog", {
      name: "Restore this object?",
    });
    await expect(confirm).toBeVisible();
    await confirm.getByRole("button", { name: /^Restore object$/ }).click();

    // The object has left the trash, so its panel closes and the URL lets it go.
    await expect(panel).toBeHidden({ timeout: SETTLE_MS });
    await expect(page).not.toHaveURL(openParamRE(obj.objectId));
    await expect(
      page.getByRole("row").filter({ hasText: obj.objectId }),
    ).toBeHidden({ timeout: SETTLE_MS });
    await expect(page.getByText(/Trash is empty/i)).toBeVisible({
      timeout: SETTLE_MS,
    });
  });

  test("purge from the panel is typed out, then the object is gone", async ({
    page,
    makeCollection,
  }) => {
    const { tenantId, collection, obj } = await trashedObject(
      page,
      makeCollection,
    );
    await gotoSettled(
      page,
      `${trashURL(tenantId, collection)}?${OPEN_PARAM}=${obj.objectId}`,
    );
    const panel = panelFor(page, obj);
    await expect(panel).toBeVisible({ timeout: SETTLE_MS });

    await panel.getByRole("button", { name: /^Purge$/ }).click();
    const confirm = page.getByRole("alertdialog", {
      name: "Permanently purge this object?",
    });
    await expect(confirm).toBeVisible();
    // A purge destroys the bytes, so it waits for the word to be typed.
    const purge = confirm.getByRole("button", { name: /^Purge permanently$/ });
    await expect(purge).toBeDisabled();
    await confirm
      .getByRole("textbox", { name: `Type ${PURGE_WORD} to confirm` })
      .fill(PURGE_WORD);
    await purge.click();

    await expect(panel).toBeHidden({ timeout: SETTLE_MS });
    await expect(page).not.toHaveURL(openParamRE(obj.objectId));
    await expect(page.getByText(/Trash is empty/i)).toBeVisible({
      timeout: SETTLE_MS,
    });

    // Gone, not merely out of the list: the link to it now finds nothing.
    await gotoSettled(
      page,
      `${trashURL(tenantId, collection)}?${OPEN_PARAM}=${obj.objectId}`,
    );
    await expect(
      page.getByText(/No object with this id is in the trash/),
    ).toBeVisible({ timeout: SETTLE_MS });
  });
});
