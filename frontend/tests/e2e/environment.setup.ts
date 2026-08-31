/**
 * A floor under an assumption the rest of the suite makes: that the
 * environment it is about to seed into is roughly empty.
 *
 * Teardown walks the RESTRICT edges and removes what each test created, so a
 * completed run leaves the counts where it found them. Nothing protects the
 * runs that do not complete. An aborted run, a manual experiment, a
 * half-finished migration — each leaves rows, and the suite keeps passing
 * until the pile is large enough to break it.
 *
 * When that happened at roughly 100 tenants it did not look like a full
 * environment. It looked like three unrelated failures: a scope assertion
 * matching the wrong "Switch Target", a trash row that had not rendered, a
 * list that stopped showing a freshly seeded tenant. Each fix moved the
 * failure one step earlier. This check exists so the next person reads "the
 * environment is full" instead of spending an afternoon on a scope picker.
 *
 * It is a warning, not a reaper. It never deletes anything: cleaning up is a
 * decision about someone else's data, and a check that deletes is a check
 * nobody dares run against a shared stack.
 */
import { test, expect } from "@playwright/test";

import { FIXTURE_SLUG_RE, uniqueSlug } from "./fixtures/unique";
import {
  countAdminMemberships,
  countFixtureShapedTenants,
  MAX_ADMIN_MEMBERSHIPS,
  MAX_LEFTOVER_TENANTS,
} from "./fixtures/seed";

test("the fixture-slug convention still holds", () => {
  // The count below is only as good as this shape. If uniqueSlug is ever
  // changed to mint something else, every leftover becomes invisible and the
  // guard silently stops guarding — so it is checked against the live
  // generator rather than trusted.
  expect(uniqueSlug("probe")).toMatch(FIXTURE_SLUG_RE);
});

test("the environment is not full of leftover fixtures", async () => {
  const { count, exceeded, sample } = await countFixtureShapedTenants();
  const how = exceeded
    ? `more than ${MAX_LEFTOVER_TENANTS} tenants`
    : `${count} tenant${count === 1 ? "" : "s"}`;
  expect(
    exceeded,
    `The test environment holds ${how} ` +
      `whose slug looks like a fixture (\`${FIXTURE_SLUG_RE.source}\`), ` +
      `for example: ${sample.join(", ")}.\n\n` +
      `These are leftovers from runs that did not finish their teardown, not ` +
      `something this suite created. Past roughly a hundred of them the suite ` +
      `starts failing in ways that look like product bugs — a scope picker ` +
      `matching the wrong row, a list that will not show a tenant you just ` +
      `seeded.\n\n` +
      `Clear them before running: recreate the compose stack ` +
      `(\`docker compose -p paladin-e2e -f tests/e2e/docker-compose.test.yaml down -v\`), ` +
      `or delete them through /trash if the stack is one you cannot drop.`,
  ).toBe(false);
});

// The check that would have caught the outage this gate was extended for.
//
// The leftover tenants above are the visible form the litter usually takes.
// The damage came from something else: memberships sharing the login
// subject. Login without a tenant hint authenticates against the five rows
// FindUsersBySubjectGlobal returns, so once the pile passed five the real
// membership fell outside the candidate set and a correct password answered
// "invalid credentials". Twenty-two specs failed on login, and the tenant
// count at the time was nowhere near its threshold — the existing check would
// have said the environment was fine.
//
// So this measures the mechanism, not a symptom: how many memberships the
// subject the suite signs in as actually holds.
test("the login subject has not accumulated memberships", async () => {
  const { subject, count, tenantIds } = await countAdminMemberships();
  expect(
    count,
    `${subject} holds ${count} memberships (tenants: ${tenantIds
      .slice(0, 6)
      .join(", ")}${tenantIds.length > 6 ? ", …" : ""}).\n\n` +
      `Login without a tenant hint checks the password against at most five ` +
      `candidate rows, so past that the real membership can be crowded out and ` +
      `a correct password is refused — the whole suite then fails on login, ` +
      `which looks like anything but a full environment.\n\n` +
      `These are leftovers from runs whose teardown did not finish. Clear them ` +
      `by recreating the compose stack ` +
      `(\`docker compose -p paladin-e2e -f tests/e2e/docker-compose.test.yaml down -v\`), ` +
      `or by deleting the extra users through /users on a stack you cannot drop.`,
  ).toBeLessThanOrEqual(MAX_ADMIN_MEMBERSHIPS);
});
