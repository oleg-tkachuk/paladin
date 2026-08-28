/**
 * Storage backend detail, and the bucket page beneath it.
 *
 * Two of the largest pages nothing had opened — 271 and 428 lines. Both are
 * platform-operator views: which buckets a backend carries, and what is
 * actually stored inside one of them. They are read-only, so what they owe the
 * operator is that the list matches reality and that an empty one says so
 * rather than looking like a page that failed to load.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";
import {
  seedAdminTenantID,
  seedPhysicalBucket,
  seedObject,
} from "./fixtures/seed";

test.describe("Storage backend detail", () => {
  test("lists the buckets the backend carries", async ({
    page,
    makeBucket,
  }) => {
    await loginAsAdmin(page);
    const bucket = await makeBucket();

    await gotoSettled(page, `/storage-backends/${bucket.backendId}`);

    await expect(
      page.getByRole("heading", { name: /Buckets on this backend/i }),
    ).toBeVisible({ timeout: 15_000 });
    // A bucket registered a moment ago is on the page: the list is the
    // backend's inventory, not a cached snapshot of one.
    await expect(page.getByText(bucket.bucketId).first()).toBeVisible({
      timeout: 15_000,
    });
  });

  test("an unknown backend does not render someone else's inventory", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    await gotoSettled(page, "/storage-backends/no-such-backend-e2e");

    // Whatever it shows, it must not show buckets — a detail page that falls
    // back to the unfiltered list is how an operator acts on the wrong one.
    await expect(page.getByText(/paladin-e2e|paladin-primary/)).toHaveCount(0);
  });
});

test.describe("Bucket contents", () => {
  test("shows what is stored, grouped tenant then Collection", async ({
    page,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    const tenantId = await seedAdminTenantID();
    const bucket = await seedPhysicalBucket();
    const collection = await makeCollection({ tenantId, bucket });
    await seedObject({ tenantId, collection: collection.collection });

    await gotoSettled(
      page,
      `/storage-backends/${bucket.backendId}/buckets/${bucket.bucketId}`,
    );

    await expect(
      page.getByRole("heading", { name: /What's stored here/i }),
    ).toBeVisible({ timeout: 15_000 });
    // The collection that owns the object appears — this page's whole claim is
    // that it can answer "who is using this bucket".
    await expect(page.getByText(collection.collection).first()).toBeVisible({
      timeout: 15_000,
    });
  });

  test("an empty bucket says it is empty", async ({ page, makeBucket }) => {
    await loginAsAdmin(page);
    const bucket = await makeBucket();

    await gotoSettled(
      page,
      `/storage-backends/${bucket.backendId}/buckets/${bucket.bucketId}`,
    );

    // The difference between "nothing here" and "nothing loaded" is the whole
    // value of an empty state on an operator page.
    await expect(
      page.getByText(/No data stored in this bucket yet/i),
    ).toBeVisible({ timeout: 15_000 });
  });
});
