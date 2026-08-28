/**
 * A bucket's Collections tab — which Collections route into this bucket.
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

function collectionsTabURL(
  tenantId: string,
  backendId: string,
  bucketId: string,
): string {
  return `/tenants/${tenantId}/buckets/${backendId}/${bucketId}/collections`;
}

test.describe("Bucket collections tab", () => {
  test("lists the Collections bound to this bucket and no others", async ({
    page,
    makeTenant,
    makeBucket,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    // A tenant of its own. These pages are addressed by tenant in the URL, so
    // nothing here needs the shared platform tenant — and staying out of it
    // keeps this spec from moving state other specs are counting on.
    const { tenantId } = await makeTenant();
    const mine = await makeBucket();
    const other = await makeBucket();
    const bound = await makeCollection({ tenantId, bucket: mine });
    const elsewhere = await makeCollection({ tenantId, bucket: other });

    await gotoSettled(
      page,
      collectionsTabURL(tenantId, mine.backendId, mine.bucketId),
    );

    await expect(
      page.getByRole("heading", { name: /^Collections$/ }),
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
    // A tenant of its own. These pages are addressed by tenant in the URL, so
    // nothing here needs the shared platform tenant — and staying out of it
    // keeps this spec from moving state other specs are counting on.
    const { tenantId } = await makeTenant();
    const bucket = await makeBucket();

    await gotoSettled(
      page,
      collectionsTabURL(tenantId, bucket.backendId, bucket.bucketId),
    );

    await expect(
      page.getByText(/No Collections bound to this bucket yet/i),
    ).toBeVisible({ timeout: 15_000 });
  });
});
