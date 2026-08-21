/**
 * US3 — Bucket list & object key open.
 *
 * Spec: specs/001-frontend-playwright-e2e/spec.md §"User Story 3".
 * The most common day-1 operator path: navigate /buckets →
 * bucket detail → Collections list → Collection detail.
 *
 * Per-test seed: a unique tenant + bucket + Collection under
 * that bucket. The bucket detail page renders Collections
 * grouped by tenant; without a seeded Collection we'd hit the
 * empty-state "No data stored in this bucket yet."
 */
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";
import { seedTenant, seedBucket, seedCollection } from "./fixtures/seed";

test.describe("US3 — Bucket list & Collection navigation", () => {
  test("bucket list shows seeded bucket", async ({ page }) => {
    await loginAsAdmin(page);
    const bucket = await seedBucket();
    await page.goto("/buckets");
    // Bucket name renders in a font-mono cell (page.tsx:417).
    // Locator on text is stable because every bucket name is
    // UUID-suffixed by seedBucket().
    await expect(page.getByText(bucket.bucketId)).toBeVisible({
      timeout: 5_000,
    });
  });

  test("bucket detail page lists Collections grouped by tenant", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const tenant = await seedTenant();
    const bucket = await seedBucket();
    const ok = await seedCollection({ tenantId: tenant.tenantId, bucket });

    // Drive through the natural operator path: /buckets →
    // click the seeded bucket row → bucket detail page.
    await page.goto("/buckets");
    const bucketLink = page.getByText(bucket.bucketId).first();
    await bucketLink.click();

    // We're now on the bucket detail page. The "What's stored
    // here" section heading + the tenant card + the Collection
    // row should all render. We assert on the Collection
    // identifier — the strongest signal that the bucket→OK
    // join made it through the UI.
    await expect(page.getByText(/What.s stored here/)).toBeVisible({
      timeout: 5_000,
    });
    await expect(page.getByText(ok.collection)).toBeVisible({
      timeout: 5_000,
    });
  });

  test("Collection detail page renders identity + canonical name", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const tenant = await seedTenant();
    const bucket = await seedBucket();
    const ok = await seedCollection({ tenantId: tenant.tenantId, bucket });

    // Direct nav — the bucket-detail "Browse" link goes to the
    // /objects browser, not the OK detail page (the Cedar
    // policy editor sits on a sibling /policy route). For
    // this test we drive straight to the detail page since
    // the spec asserts on Identity + canonical name, both of
    // which live on /tenants/{id}/collections/{name}.
    await page.goto(
      `/tenants/${encodeURIComponent(tenant.slug)}/collections/${encodeURIComponent(ok.collection)}`,
    );

    // Identity card heading + the canonical resource name
    // (e.g. "tenants/<uuid>/collections/e2e/abc12345") rendered
    // in a font-mono <dd>. The display name from seedCollection
    // appears too in the editable name input.
    await expect(page.getByRole("heading", { name: "Identity" })).toBeVisible({
      timeout: 5_000,
    });
    // The collection string appears in several places (breadcrumb, name
    // input, canonical resource name); .first() is enough to confirm the
    // page resolved the (slash-containing) key and rendered.
    await expect(page.getByText(ok.collection).first()).toBeVisible({
      timeout: 5_000,
    });
    // tenants/<id>/collections/<key> is the canonical shape —
    // assert the literal `tenants/` prefix appears alongside
    // our key so we know we're looking at the resource-name
    // string, not just a coincidental match elsewhere.
    await expect(page.locator(`text=/tenants/.*${ok.collection}/`)).toBeVisible(
      {
        timeout: 5_000,
      },
    );
  });
});
