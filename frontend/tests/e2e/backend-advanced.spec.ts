/**
 * Storage backend Edit dialog — the Advanced section (sse / events /
 * cedar_policy).
 *
 * These three are mask-gated as whole GROUPS on the server: naming "events"
 * writes enabled, target, queue_url AND poll_interval from the message, in one
 * statement. So the dangerous edit is not the one that sets them — it is the
 * one that does not. A dialog that always sent the groups would let "rename the
 * backend" blank a KMS key id or an SQS queue URL, and the operator would have
 * no reason to look.
 *
 * The hook's unit tests assert the mask the console builds. This asserts what
 * the SERVER ends up holding after a real click, which is the only place the
 * two can disagree.
 *
 * backend-lifecycle.spec.ts covers drain/maintenance; storage-backends-detail
 * covers the inventory. This is the Edit dialog.
 */
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";
import { backendAdvanced, setBackendAdvanced } from "./fixtures/seed";

// Mirrors paladin.admin.v1.SseType / EventTarget. Spelled out rather than
// imported so a renumbering of the enum fails this test loudly instead of
// silently agreeing with itself.
const SSE_KMS = 3;
const EVENT_TARGET_SQS = 2;

test.describe("Storage backend — advanced fields", () => {
  // What the dirty-tracking actually controls is the MASK, so the mask is what
  // this asserts — read off the wire, not inferred from the row afterwards.
  //
  // Inferring it does not work, and the first version of this test made exactly
  // that mistake. A freshly created backend already carries sse.type=NONE and
  // events.target=NONE (not UNSPECIFIED, as the seed shows), the form pre-fills
  // from those, and sending the group back writes the same values. So a dialog
  // that always sent them would leave a database row byte-identical and pass
  // any check that only looks at the row. The difference is visible in one
  // place: whether the request names the group at all.
  test("an unrelated edit does not name the advanced groups in the mask", async ({
    page,
    makeEnabledBackend,
  }) => {
    await loginAsAdmin(page);
    const be = await makeEnabledBackend();

    await gotoSettled(page, `/storage-backends/${be.backendId}`);
    await page.getByRole("button", { name: /^Edit$/ }).click();
    const displayName = page.getByLabel(/display name/i);
    await expect(displayName).toBeVisible({ timeout: 15_000 });
    await displayName.fill("Renamed, nothing else");

    // The console talks to the planes through its own BFF at /api/rpc/<proc>.
    const request = page.waitForRequest(
      (r) => r.url().includes("/api/rpc/") && r.url().includes("UpdateBackend"),
      { timeout: 15_000 },
    );
    await page.getByRole("button", { name: /save changes/i }).click();
    const body = JSON.parse((await request).postData() ?? "{}");

    // A FieldMask on the wire is a comma-separated string of camelCase names,
    // not the {paths: []} object it is in code — canonical protojson form, and
    // the same shape dev-bootstrap.sh had to learn the hard way.
    expect(body.updateMask).toBe(
      "displayName,endpoint,publicEndpoint,region,forcePathStyle",
    );
    // Absent from the message too, not merely from the mask: a server that
    // ever widened its mask handling still could not read a value this caller
    // never sent.
    expect(body.backend.sse).toBeUndefined();
    expect(body.backend.events).toBeUndefined();
  });

  // The outcome, where the test above is the mechanism. Weaker on purpose and
  // worth saying so: because the round trip through the form is lossless for
  // every value the form can represent, this one would still pass if the
  // dirty-tracking were removed. It is here for the case the mask test cannot
  // see — a future enum member this build does not know, which collapses to
  // UNSPECIFIED on the way through and would show up as a changed row.
  test("an edit that does not touch them leaves sse and events alone", async ({
    page,
    makeEnabledBackend,
  }) => {
    await loginAsAdmin(page);
    const be = await makeEnabledBackend();

    // Establish a prior state the way an operator would have: set once, then
    // left alone for months.
    await setBackendAdvanced(be.backendId, {
      sse: { type: SSE_KMS, keyId: "arn:aws:kms:eu-north-1:key/abc123" },
      events: {
        enabled: true,
        target: EVENT_TARGET_SQS,
        queueUrl: "https://sqs.eu-north-1.amazonaws.com/1/paladin",
        pollMs: 20_000,
      },
      cedarPolicy: "forbid(principal, action, resource) when { true };",
    });
    const before = await backendAdvanced(be.backendId);
    expect(before.sseKeyId).toBe("arn:aws:kms:eu-north-1:key/abc123");

    await gotoSettled(page, `/storage-backends/${be.backendId}`);
    await page.getByRole("button", { name: /^Edit$/ }).click();

    const displayName = page.getByLabel(/display name/i);
    await expect(displayName).toBeVisible({ timeout: 15_000 });
    await displayName.fill("Renamed, nothing else");
    await page.getByRole("button", { name: /save changes/i }).click();

    await expect
      .poll(async () => (await backendAdvanced(be.backendId)).sseKeyId, {
        timeout: 15_000,
      })
      .toBe("arn:aws:kms:eu-north-1:key/abc123");

    const after = await backendAdvanced(be.backendId);
    expect(after.sseType).toBe(before.sseType);
    expect(after.eventsEnabled).toBe(before.eventsEnabled);
    expect(after.eventsTarget).toBe(before.eventsTarget);
    expect(after.eventsQueueUrl).toBe(before.eventsQueueUrl);
    expect(after.eventsPollMs).toBe(before.eventsPollMs);
    expect(after.cedarPolicy).toBe(before.cedarPolicy);
  });

  test("editing the Cedar policy writes it, and only it", async ({
    page,
    makeEnabledBackend,
  }) => {
    await loginAsAdmin(page);
    const be = await makeEnabledBackend();
    await setBackendAdvanced(be.backendId, {
      sse: { type: SSE_KMS, keyId: "arn:aws:kms:eu-north-1:key/keep-me" },
    });

    await gotoSettled(page, `/storage-backends/${be.backendId}`);
    await page.getByRole("button", { name: /^Edit$/ }).click();

    // The section is collapsed by default — an operator opens it deliberately,
    // and so does this.
    await page.getByText(/^Advanced —/).click();

    const policy = page.getByLabel(/cedar policy/i);
    await expect(policy).toBeVisible({ timeout: 15_000 });
    await policy.fill("permit(principal, action, resource) when { false };");
    await page.getByRole("button", { name: /save changes/i }).click();

    await expect
      .poll(async () => (await backendAdvanced(be.backendId)).cedarPolicy, {
        timeout: 15_000,
      })
      .toContain("permit(principal, action, resource)");

    // The neighbouring group went untouched, which is the half a "did the save
    // work" assertion would miss.
    expect((await backendAdvanced(be.backendId)).sseKeyId).toBe(
      "arn:aws:kms:eu-north-1:key/keep-me",
    );
  });

  test("a sub-second poll interval survives the Duration round trip", async ({
    page,
    makeEnabledBackend,
  }) => {
    await loginAsAdmin(page);
    const be = await makeEnabledBackend();

    await gotoSettled(page, `/storage-backends/${be.backendId}`);
    await page.getByRole("button", { name: /^Edit$/ }).click();
    await page.getByText(/^Advanced —/).click();

    await page.getByRole("checkbox", { name: /ingest events/i }).check();
    // 1500ms is the value a seconds-only conversion loses in either direction:
    // truncation gives 1s, rounding gives 2s, and neither is what was typed.
    await page.getByLabel(/poll interval/i).fill("1500");
    await page.getByRole("button", { name: /save changes/i }).click();

    await expect
      .poll(async () => (await backendAdvanced(be.backendId)).eventsPollMs, {
        timeout: 15_000,
      })
      .toBe(1500);
    expect((await backendAdvanced(be.backendId)).eventsEnabled).toBe(true);
  });

  test("KMS without a key id cannot be saved", async ({
    page,
    makeEnabledBackend,
  }) => {
    await loginAsAdmin(page);
    const be = await makeEnabledBackend();

    await gotoSettled(page, `/storage-backends/${be.backendId}`);
    await page.getByRole("button", { name: /^Edit$/ }).click();
    await page.getByText(/^Advanced —/).click();

    // Radix's Select is a listbox, not a <select>: open it, then pick.
    await page.getByRole("combobox").first().click();
    await page.getByRole("option", { name: /KMS/ }).click();

    const key = page.getByLabel(/kms key id/i);
    await expect(key).toBeVisible({ timeout: 15_000 });
    await expect(key).toHaveValue("");
    // The server answers InvalidArgument for this; refusing here means the
    // operator learns it without a round trip.
    await expect(
      page.getByRole("button", { name: /save changes/i }),
    ).toBeDisabled();
  });
});
