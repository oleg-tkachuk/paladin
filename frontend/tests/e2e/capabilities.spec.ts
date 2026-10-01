/**
 * US4 — Capability create + revoke + Idempotency contract.
 *
 * Spec: specs/001-frontend-playwright-e2e/spec.md §"User Story 4".
 * The critical scenario in this file is T032 — the FR-008
 * regression guard for the middleware reflective-replay layer
 * (backend internal/middleware/idempotency.go) that shipped
 * with this feature. If RequireOnCreate quietly flips to false
 * OR isMutationMethod stops matching `Issue*`, T032 fails.
 *
 * Idempotency gate coverage notes:
 *   - CapabilityService.Issue is named Issue, not Create*.
 *     During /speckit-implement Phase 6 the matcher was
 *     extended to recognise both prefixes (spec.md
 *     Clarifications Q8). Frontend transport + backend
 *     middleware both updated; this test exercises both
 *     ends.
 *
 * Per-test seed: one tenant. Capabilities are seeded via the
 * direct Connect-RPC client (fixtures/seed.ts) so we can pin
 * the Idempotency-Key value — the UI form's auto-injected
 * UUID is opaque to the test.
 */
import { Code } from "@connectrpc/connect";
import { test, expect } from "./fixtures/resources";
import { loginAsAdmin } from "./fixtures/auth";
import { gotoSettled } from "./fixtures/navigate";
import { seedCapability } from "./fixtures/seed";

// /tenants/<id>/capabilities is browse-scoped (CapabilityService.List is
// principal-scoped at the SQL level): it lists nothing until you pick a
// (principal_kind, subject). principalKind defaults to AGENT — the kind
// seedCapability uses — so filling the subject filter is enough to list the
// seeded principal's capabilities. The result table shows the capability ID
// (there is no subject column), so assertions match on cap.id, not cap.subject.
async function browseCapabilities(
  page: import("@playwright/test").Page,
  subject: string,
) {
  await page.getByPlaceholder(/agent-id/).fill(subject);
}

test.describe("US4 — Capability lifecycle + FR-008 idempotency", () => {
  test("seeded capability appears on /tenants/<id>/capabilities", async ({
    page,
    makeTenant,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();
    const cap = await seedCapability({ tenantId: tenant.tenantId });

    await gotoSettled(
      page,
      `/tenants/${encodeURIComponent(tenant.slug)}/capabilities`,
    );
    await browseCapabilities(page, cap.subject);
    // The table renders the capability ID (no subject column); match on it.
    await expect(page.getByText(cap.id).first()).toBeVisible({
      timeout: 10_000,
    });
  });

  test("double-submit with the same Idempotency-Key issues one capability (FR-008)", async ({
    page,
    makeTenant,
  }) => {
    // Two Issue calls with one key must not issue two capabilities. Driven
    // through Connect directly, not a UI double-click: a submit button that
    // disables on the first click would pass without the second request ever
    // reaching the server.
    //
    // The second call is REFUSED rather than replayed. Issue's response
    // carries the capability token, and the idempotency cache does not store
    // a credential, so it cannot hand the same response back; it answers
    // AlreadyExists, and the caller uses a new key to issue another
    // (backend/internal/middleware/idempotency.go, refuseCredentialReplay).

    await loginAsAdmin(page);
    const tenant = await makeTenant();

    // Same key for both calls — that's the entire point.
    const sharedKey = crypto.randomUUID();
    const first = await seedCapability({
      tenantId: tenant.tenantId,
      subjectPrefix: "e2e-twin",
      idempotencyKey: sharedKey,
    });
    const second = seedCapability({
      tenantId: tenant.tenantId,
      subjectPrefix: "e2e-twin",
      idempotencyKey: sharedKey,
    });
    await expect(second).rejects.toMatchObject({ code: Code.AlreadyExists });

    // And the UI shows the one capability. exact:true matches the ID cell
    // only — not the "Actions for capability <id>" sr-only label.
    await gotoSettled(
      page,
      `/tenants/${encodeURIComponent(tenant.slug)}/capabilities`,
    );
    await browseCapabilities(page, first.subject);
    await expect(page.getByText(first.id, { exact: true })).toHaveCount(1, {
      timeout: 10_000,
    });
  });

  test("revoke flips capability status without removing the row", async ({
    page,
    makeTenant,
  }) => {
    await loginAsAdmin(page);
    const tenant = await makeTenant();
    const cap = await seedCapability({ tenantId: tenant.tenantId });

    await gotoSettled(
      page,
      `/tenants/${encodeURIComponent(tenant.slug)}/capabilities`,
    );
    await browseCapabilities(page, cap.subject);
    await expect(page.getByText(cap.id).first()).toBeVisible({
      timeout: 10_000,
    });

    // Trigger the per-row dropdown. The trigger has an
    // sr-only span "Actions for capability <id>" — fully
    // unambiguous because the cap id is UUID.
    const actionsTrigger = page.getByRole("button", {
      name: new RegExp(`Actions for capability ${cap.id}`),
    });
    await actionsTrigger.click();

    // The Revoke menu item lives below the divider in the
    // Dropdown.Menu. Match by text — the only "Revoke"
    // affordance on this page.
    await (async () => {
      const item = page.getByRole("menuitem", { name: /Revoke/ });
      await expect(item).toBeVisible({ timeout: 15_000 });
      await item.click({ timeout: 20_000 });
    })();

    // Confirm in the dialog. The dialog footer has a
    // destructive-styled confirm button; matched by role +
    // accessible name.
    await page.getByRole("button", { name: /^Revoke$/ }).click();

    // The list defaults to includeRevoked=false, so the row would DISAPPEAR
    // after revoke without an explicit toggle. Flip the filter so we can
    // assert the row remains AND its status is "revoked" (US4 scenario 3).
    // The checkbox's accessible name is its label text — "revoked".
    await page.getByRole("checkbox", { name: /^revoked$/i }).check();

    // The row is still present, now in revoked state — match on the ID
    // (still rendered) plus the "Revoked" status badge.
    await expect(page.getByText(cap.id).first()).toBeVisible({
      timeout: 5_000,
    });
    await expect(page.getByText(/Revoked/i).first()).toBeVisible({
      timeout: 5_000,
    });
  });
});
