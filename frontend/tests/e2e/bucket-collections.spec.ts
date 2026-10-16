/**
 * A bucket's Collections tab — which Collections route into this bucket, by
 * tenant, on the bucket's one page (ADR-0033).
 *
 * The last of the seven pages nothing had opened. It answers a question the
 * bucket settings pages raise and do not resolve: before changing a bucket's
 * lifecycle, versioning or policy, an operator wants to know what is bound to
 * it. A tab that lists the wrong Collections — or all of them — turns that
 * check into a false reassurance.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";

// A bucket's one page, under its backend (ADR-0033).
function collectionsTabURL(backendId: string, bucketId: string): string {
  return `/storage-backends/${backendId}/buckets/${bucketId}/collections`;
}

test.describe("Bucket collections tab", () => {
  test("lists the Collections bound to this bucket and no others", async ({
    page,
    makeTenant,
    makeBucket,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    // A tenant of its own: staying out of the shared platform tenant keeps
    // this spec from moving state other specs are counting on.
    const { tenantId } = await makeTenant();
    // ADR-0032: a collection binds only to a bucket its tenant owns or was
    // granted.
    const mine = await makeBucket({ ownerTenantId: tenantId });
    const other = await makeBucket({ ownerTenantId: tenantId });
    const bound = await makeCollection({ tenantId, bucket: mine });
    const elsewhere = await makeCollection({ tenantId, bucket: other });

    await gotoSettled(page, collectionsTabURL(mine.backendId, mine.bucketId));

    await expect(
      page.getByRole("tab", { name: "Collections", selected: true }),
    ).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.getByText(bound.collection).first()).toBeVisible({
      timeout: 15_000,
    });
    // The second Collection belongs to a different bucket. Its absence here is
    // the whole point of the tab — the filter is applied client-side, which is
    // exactly the kind of thing that quietly stops filtering.
    await expect(page.getByText(elsewhere.collection)).toHaveCount(0);
  });

  test("a bucket nothing is bound to says so", async ({
    page,
    makeTenant,
    makeBucket,
  }) => {
    await loginAsAdmin(page);
    // A tenant of its own: staying out of the shared platform tenant keeps
    // this spec from moving state other specs are counting on.
    const { tenantId } = await makeTenant();
    const bucket = await makeBucket({ ownerTenantId: tenantId });

    await gotoSettled(
      page,
      collectionsTabURL(bucket.backendId, bucket.bucketId),
    );

    await expect(
      page.getByText(/No collection is bound to this bucket yet/i),
    ).toBeVisible({ timeout: 15_000 });
  });
});
