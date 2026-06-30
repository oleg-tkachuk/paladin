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
import { test, expect } from "@playwright/test";
import { loginAsAdmin } from "./fixtures/auth";
import { seedTenant, seedCapability } from "./fixtures/seed";

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
  }) => {
    await loginAsAdmin(page);
    const tenant = await seedTenant();
    const cap = await seedCapability({ tenantId: tenant.tenantId });

    await page.goto(`/tenants/${encodeURIComponent(tenant.slug)}/capabilities`);
    await browseCapabilities(page, cap.subject);
    // The table renders the capability ID (no subject column); match on it.
    await expect(page.getByText(cap.id).first()).toBeVisible({
      timeout: 10_000,
    });
  });

  test("double-submit with the same Idempotency-Key collapses (FR-008)", async ({
    page,
  }) => {
    // This is the single most important test in the suite. It
    // verifies the middleware reflective-replay layer
    // (backend/internal/middleware/idempotency.go,
    // reconstructResponse) does its job: two Issue calls with
    // the same key MUST return the same capability ID.
    //
    // Per /speckit-analyze's C2 finding, a UI-only double-click
    // would silently pass if the submit button disables on
    // first click (the second click no-ops, only one network
    // request fires, idempotency layer is never exercised).
    // Driving via direct Connect-RPC with a pinned key bypasses
    // that hazard — both calls reach the server.

    await loginAsAdmin(page);
    const tenant = await seedTenant();

    // Same key for both calls — that's the entire point.
    const sharedKey = crypto.randomUUID();
    const first = await seedCapability({
      tenantId: tenant.tenantId,
      subjectPrefix: "e2e-twin",
      idempotencyKey: sharedKey,
    });
    const second = await seedCapability({
      tenantId: tenant.tenantId,
      subjectPrefix: "e2e-twin",
      idempotencyKey: sharedKey,
    });

    // The middleware MUST return the same capability ID for
    // both calls — replay path. If RequireOnCreate were off OR
    // isMutationMethod stopped matching Issue, the second call
    // would create a NEW capability (different ID, different
    // subject because subjectPrefix uniqueSlug runs twice).
    expect(second.id).toEqual(first.id);
    // Belt-and-braces: list UI shows only one row matching
    // the prefix. If two rows appear, the middleware
    // memoization regressed.
    await page.goto(`/tenants/${encodeURIComponent(tenant.slug)}/capabilities`);
    const twinRows = page.getByText(/e2e-twin-/);
    await expect(twinRows).toHaveCount(1, { timeout: 5_000 });
  });

  test("revoke flips capability status without removing the row", async ({
    page,
  }) => {
    await loginAsAdmin(page);
    const tenant = await seedTenant();
    const cap = await seedCapability({ tenantId: tenant.tenantId });

    await page.goto(`/tenants/${encodeURIComponent(tenant.slug)}/capabilities`);
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
    await page.getByRole("menuitem", { name: /Revoke/ }).click();

    // Confirm in the dialog. The dialog footer has a
    // destructive-styled confirm button; matched by role +
    // accessible name.
    await page.getByRole("button", { name: /^Revoke$/ }).click();

    // The list defaults to includeRevoked=false (page.tsx:603),
    // so the row would DISAPPEAR after revoke without an
    // explicit toggle. Flip the "include revoked" filter so
    // we can assert the row remains AND its status is
    // "revoked" (per US4 acceptance scenario 3).
    await page.getByRole("checkbox", { name: /include revoked/i }).check();

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
