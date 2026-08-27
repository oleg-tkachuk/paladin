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
import { test, expect, type MakeCollection } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import {
  seedAdminTenantID,
  seedObject,
  seedPhysicalBucket,
  softDeleteObject,
} from "./fixtures/seed";
import { gotoSettled } from "./fixtures/navigate";

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
