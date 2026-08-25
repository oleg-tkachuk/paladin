/**
 * US6 — Object lifecycle.
 *
 * The path the product exists for, and the one the suite did not cover: an
 * object is uploaded, listed, soft-deleted, found in the trash, restored,
 * and listed again. Everything before this file exercised the admin surface
 * — tenants, buckets, scope, capabilities — and left the data plane to unit
 * tests, which cannot see the console.
 *
 * Per-test seed: a tenant, a bucket, a collection under it, and objects
 * created through the real two-step upload (UploadObject → presigned PUT →
 * CompleteObject). Nothing here writes to Postgres directly, so a break
 * anywhere along that chain fails the test rather than being papered over
 * by a hand-inserted row.
 */
import { test, expect, type Page } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";
import {
  seedPhysicalBucket,
  seedCollection,
  seedObject,
  seedMultipartObject,
  softDeleteObject,
  seedAdminTenantID,
} from "./fixtures/seed";

/**
 * Seeds a collection under the ADMIN's own tenant.
 *
 * Not under a fresh tenant, which was the first shape this took: the data
 * plane scopes every request to the caller's tenant, so uploading into
 * another tenant's collection fails with "collection not found" — RLS
 * hiding a row that exists. That is the isolation working. A data-plane
 * test therefore runs where its token can see, and cross-tenant behaviour
 * is asserted on the admin plane, where crossing is the point.
 */
async function seedScope() {
  const tenantId = await seedAdminTenantID();
  // The physical bucket: these tests move real bytes.
  const bucket = await seedPhysicalBucket();
  const collection = await seedCollection({ tenantId, bucket });
  return {
    tenantId,
    tenantSlug: "platform",
    bucket,
    collection: collection.collection,
  };
}

function objectsURL(tenantSlug: string, collection: string): string {
  return `/tenants/${encodeURIComponent(tenantSlug)}/collections/${encodeURIComponent(collection)}/objects`;
}

/**
 * The table shows an object's BASE name in its cell — `obj-1234.txt` — while
 * the full key, prefix included, lives in the row's accessible name ("Select
 * object e2e/obj-1234.txt"). Matching the row rather than a text node is
 * therefore both what a screen-reader user hears and the only locator that
 * sees the whole key.
 */
function objectRow(page: Page, key: string) {
  return page.getByRole("row", { name: new RegExp(escapeRe(key)) });
}

async function expectKeyVisible(page: Page, key: string) {
  await expect(objectRow(page, key)).toBeVisible({ timeout: 10_000 });
}

test.describe("US6 — Object lifecycle", () => {
  test("an uploaded object appears in its collection's object list", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const { tenantId, tenantSlug, collection } = await seedScope();
    const obj = await seedObject({ tenantId, collection });

    await gotoSettled(page, objectsURL(tenantSlug, collection));
    await expectKeyVisible(page, obj.key);
  });

  test("the object list is scoped to its collection", async ({ page }) => {
    await loginAsAdmin(page);
    const a = await seedScope();
    const b = await seedScope();
    const inA = await seedObject({
      tenantId: a.tenantId,
      collection: a.collection,
    });
    const inB = await seedObject({
      tenantId: b.tenantId,
      collection: b.collection,
    });

    // Two collections, same tenant: this isolates the COLLECTION filter,
    // which no tenant-level check would catch. A listing that stopped
    // narrowing by collection shows more data than expected rather than
    // erroring, so only an assertion like this notices.
    await gotoSettled(page, objectsURL(a.tenantSlug, a.collection));
    await expectKeyVisible(page, inA.key);
    await expect(objectRow(page, inB.key)).toHaveCount(0);
  });

  test("a soft-deleted object leaves the list and appears in the trash", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const { tenantSlug, tenantId, collection } = await seedScope();
    const obj = await seedObject({ tenantId, collection });

    await gotoSettled(page, objectsURL(tenantSlug, collection));
    await expectKeyVisible(page, obj.key);

    // Deleted through the data plane rather than the row menu: this test is
    // about where a deleted object ENDS UP, and driving the dropdown would
    // make it fail for reasons that have nothing to do with that. The menu
    // itself is worth its own test.
    await softDeleteObject(obj.name);

    await page.reload();
    await expect(objectRow(page, obj.key)).toHaveCount(0, { timeout: 10_000 });

    // Present in the collection's trash, which is what makes the deletion
    // recoverable rather than merely slow: the bytes stay in the backend
    // until the lifecycle reaper runs.
    await page.goto(
      `/tenants/${encodeURIComponent(tenantSlug)}/collections/${encodeURIComponent(collection)}/trash`,
    );
    await expectKeyVisible(page, obj.key);
  });

  test("the row menu soft-deletes the object", async ({ page }) => {
    await loginAsAdmin(page);
    const { tenantSlug, tenantId, collection } = await seedScope();
    const obj = await seedObject({ tenantId, collection });

    await gotoSettled(page, objectsURL(tenantSlug, collection));
    await expectKeyVisible(page, obj.key);

    // The trigger is icon-only, so its accessible name is the only thing
    // identifying which row's menu this is — "Actions for <key>". Before
    // this was added the control announced as a bare "button", which left a
    // screen-reader user with no way to tell two rows' menus apart, and no
    // way to write this assertion either.
    await objectRow(page, obj.key)
      .getByRole("button", { name: `Actions for ${obj.key}` })
      .click();
    await page.getByText("Soft Delete", { exact: true }).click();

    await expect(objectRow(page, obj.key)).toHaveCount(0, { timeout: 10_000 });
  });

  test("a multipart upload lands as one object in the list", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const { tenantSlug, tenantId, collection } = await seedScope();

    // Two parts: past the 5 MiB floor S3 imposes on every part but the last,
    // so the server splits it and Complete has more than one ETag to
    // reconcile. A single-part "multipart" upload would pass without
    // exercising the reconciliation that actually breaks.
    const big = await seedMultipartObject({
      tenantId,
      collection,
      sizeBytes: 12 * 1024 * 1024,
    });

    await gotoSettled(page, objectsURL(tenantSlug, collection));
    // One row, not one per part: the parts are an implementation detail of
    // the transfer, and a listing that showed them would mean Complete did
    // not merge them.
    await expectKeyVisible(page, big.key);
    await expect(objectRow(page, big.key)).toHaveCount(1);
  });
});

/** Keys contain `/` and `.`; escape before building a row-name regex. */
function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
