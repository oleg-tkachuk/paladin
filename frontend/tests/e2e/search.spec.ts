/**
 * List search — the query reaches the server, and the server answers it.
 *
 * The console used to fetch every row and narrow the array in the browser.
 * Moving that to the API is only correct if three things hold at once, and
 * only a real Postgres can show all three: the filter is a CEL expression the
 * plane accepts, the match is case-insensitive the way the browser's
 * `toLowerCase().includes()` was, and it covers the display name and not just
 * the id.
 *
 * The unit tests assert what the console SENDS. These assert what comes back,
 * which is the half that can disagree — a filter the server rejects, or a
 * `COLLATE` that folds differently from Go, both look like a search box that
 * finds nothing.
 *
 * The /collections case is a regression test with a history: that page passed
 * the box contents straight into `filter`, so typing anything sent an
 * undeclared CEL identifier, the plane answered InvalidArgument, and the list
 * emptied. Nothing here noticed, because no e2e test had ever typed into a
 * search box.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";

// The console talks to the planes through its own BFF at /api/rpc/<proc>.
const rpc = (proc: string) => (r: { url(): string }) =>
  r.url().includes("/api/rpc/") && r.url().includes(proc);

test.describe("List search", () => {
  test("a bucket search matches the display name, ignoring case", async ({
    page,
    makeBucket,
  }) => {
    await loginAsAdmin(page);
    // Two buckets. The needle is distinguishable only by its DISPLAY name, so
    // a filter that looked at the id alone would fail here — which is what the
    // first draft of the derived `search` field did.
    const needle = await makeBucket({ displayNamePrefix: "Zqx Needle" });
    const other = await makeBucket({ displayNamePrefix: "Unrelated" });

    await gotoSettled(page, "/buckets");
    await expect(page.getByText(other.bucketId)).toBeVisible({
      timeout: 15_000,
    });

    // UPPERCASE on purpose: the display name is mixed case, the console folds
    // ASCII before sending, and Postgres folds the column the same way. Any
    // of the three getting it wrong shows up as an empty table.
    const request = page.waitForRequest(rpc("ListBuckets"), {
      timeout: 15_000,
    });
    await page.getByPlaceholder(/search by name/i).fill("ZQX NEEDLE");
    const body = JSON.parse((await request).postData() ?? "{}");

    // It went to the SERVER, as an expression — not as the raw box contents,
    // and not as a disjunction (which pushes nothing into SQL, so a match past
    // the first page would be reported as absent).
    expect(body.filter).toBe('search.contains("zqx needle")');
    expect(body.filter).not.toContain("||");

    await expect(page.getByText(needle.bucketId)).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.getByText(other.bucketId)).toHaveCount(0);
  });

  // Split in two on purpose, because the two pages can prove different things.
  //
  // /collections lists the SIGNED-IN user's own tenant, which this test does
  // not control — seeding a collection under a fresh tenant puts it somewhere
  // that page never shows. So this asserts the thing that was actually broken
  // there: the shape of what goes on the wire.
  test("the cross-tenant list sends an expression, not the raw box contents", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    await gotoSettled(page, "/collections");

    const request = page.waitForRequest(rpc("ListCollections"), {
      timeout: 15_000,
    });
    await page.getByPlaceholder(/search by name/i).fill("zqxfind");
    const body = JSON.parse((await request).postData() ?? "{}");

    // The regression: "zqxfind" arriving as the filter itself. The plane
    // compiles that as CEL, finds no such identifier, answers InvalidArgument,
    // and the list empties for a reason the operator cannot see.
    expect(body.filter).not.toBe("zqxfind");
    expect(body.filter).toBe('search.contains("zqxfind")');
  });

  // The tenant-scoped list, where the test owns the tenant and can therefore
  // assert what comes BACK — the half a wire assertion cannot reach. This page
  // searched `collection.startsWith(...)`, case-sensitively, over the name
  // only; a substring of the middle of the name proves all three changed.
  test("a tenant's collection list finds a substring of a seeded name", async ({
    page,
    makeTenant,
    makeBucket,
    makeCollection,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();
    const bucket = await makeBucket();
    const collection = await makeCollection({
      tenantId: tenant.tenantId,
      bucket,
    });

    await gotoSettled(page, `/tenants/${tenant.tenantId}/collections`);
    await expect(page.getByText(collection.collection)).toBeVisible({
      timeout: 15_000,
    });

    // seedCollection names collections `e2e/<hex>` — it strips whatever prefix
    // it was given — so the needle is taken from the name it actually made.
    // The middle of the hex, never its start: a prefix match would pass here
    // by accident and prove nothing about the change.
    const hex = collection.collection.split("/")[1];
    const middle = hex.slice(2, 6).toUpperCase();

    await page.getByPlaceholder(/search by name/i).fill(middle);
    await expect(page.getByText(collection.collection)).toBeVisible({
      timeout: 15_000,
    });
  });
});
