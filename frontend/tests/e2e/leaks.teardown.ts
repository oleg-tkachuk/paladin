/**
 * The run left nothing behind.
 *
 * Runs after every test, as the environment project's teardown, and compares
 * the stack against the inventory the environment project took at the start
 * (fixtures/leaks.ts). A bucket, backend or active tenant that is new since
 * then is one a teardown failed to remove, and fails here by name: each
 * bucket holds SeaweedFS volume slots, and leaked ones are what filled the
 * store until it refused new buckets.
 *
 * Tenants new in the trash are reported, not failed on: a tenant with charges
 * keeps them by product rule (charges.tenant_id is RESTRICT), so the trash is
 * where it honestly ends.
 */
import { readFile } from "node:fs/promises";
import path from "node:path";

import { test, expect } from "@playwright/test";

import {
  LEAK_BASELINE_FILE,
  addedSince,
  fixtureInventory,
  type FixtureInventory,
} from "./fixtures/leaks";

/** The environment project's name, whose output holds the baseline. */
const ENVIRONMENT_PROJECT = "environment";

test("the run removed what it created", async ({}, testInfo) => {
  const environment = testInfo.config.projects.find(
    (p) => p.name === ENVIRONMENT_PROJECT,
  );
  expect(environment, "the environment project is configured").toBeDefined();
  const before = JSON.parse(
    await readFile(
      path.join(environment!.outputDir, LEAK_BASELINE_FILE),
      "utf8",
    ),
  ) as FixtureInventory;
  const added = addedSince(before, await fixtureInventory());

  if (added.trashedTenants.length > 0) {
    console.warn(
      `[leaks] tenants left in the trash: ${added.trashedTenants.join(", ")}`,
    );
  }
  expect(
    {
      activeTenants: added.activeTenants,
      buckets: added.buckets,
      backends: added.backends,
    },
    "resources this run created and its teardown did not remove",
  ).toEqual({ activeTenants: [], buckets: [], backends: [] });
});
