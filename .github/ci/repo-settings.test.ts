import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { parse } from "yaml";

import {
  ADMIN_ROLE_ID,
  CHECKS_RULESET_NAME,
  LEGACY_RULESET_NAME,
  MAIN_REF,
  REQUIRED_CONTEXTS,
  RULESET_NAME,
  checksRuleset,
  mainRuleset,
  splitRefs,
  upsertRuleset,
  type Ruleset,
  type RulesetsApi,
} from "./repo-settings.ts";

// A required context no job reports blocks every merge, silently: each one
// is a job's name in ci.yaml.
test("every required context is a job ci.yaml runs", () => {
  const ci = parse(readFileSync(new URL("../workflows/ci.yaml", import.meta.url), "utf8")) as { jobs: Record<string, { name?: string }> };
  const names = Object.values(ci.jobs).map((j) => j.name);
  for (const context of REQUIRED_CONTEXTS) assert.ok(names.includes(context), context);
});

test("main is protected from deletion", () => {
  assert.deepEqual(mainRuleset().conditions.ref_name.include, [MAIN_REF]);
  assert.deepEqual(mainRuleset().rules, [{ type: "deletion" }]);
});

test("the checks are required up to date, with the admin able to bypass them always", () => {
  const r = checksRuleset([MAIN_REF]);
  assert.equal(r.name, CHECKS_RULESET_NAME);
  assert.deepEqual(r.bypass_actors, [{ actor_id: ADMIN_ROLE_ID, actor_type: "RepositoryRole", bypass_mode: "always" }]);
  assert.deepEqual(r.rules, [
    {
      type: "required_status_checks",
      parameters: { strict_required_status_checks_policy: true, required_status_checks: REQUIRED_CONTEXTS.map((context) => ({ context })) },
    },
  ]);
});

test("CHECKS_REFS is read as white-space-separated refs, main by default", () => {
  assert.deepEqual(splitRefs(undefined), [MAIN_REF]);
  assert.deepEqual(splitRefs("  "), [MAIN_REF]);
  assert.deepEqual(splitRefs("refs/heads/main  refs/heads/next"), ["refs/heads/main", "refs/heads/next"]);
});

// fake is a rulesets API over a list, logging what it was asked.
function fake(existing: { id: number; name: string }[]) {
  const calls: string[] = [];
  const api: RulesetsApi = {
    list: async () => existing,
    create: async (r: Ruleset) => void calls.push(`create ${r.name}`),
    update: async (id: number, r: Ruleset) => void calls.push(`update ${id} ${r.name}`),
  };
  return { api, calls };
}

test("a ruleset is updated by its name, else by a former name, else created", async () => {
  let f = fake([{ id: 7, name: RULESET_NAME }, { id: 8, name: LEGACY_RULESET_NAME }]);
  await upsertRuleset(f.api, mainRuleset(), [LEGACY_RULESET_NAME]);
  assert.deepEqual(f.calls, [`update 7 ${RULESET_NAME}`]);

  f = fake([{ id: 8, name: LEGACY_RULESET_NAME }]);
  await upsertRuleset(f.api, mainRuleset(), [LEGACY_RULESET_NAME]);
  assert.deepEqual(f.calls, [`update 8 ${RULESET_NAME}`], "the legacy one is renamed in place");

  f = fake([{ id: 9, name: "other" }]);
  await upsertRuleset(f.api, mainRuleset(), [LEGACY_RULESET_NAME]);
  assert.deepEqual(f.calls, [`create ${RULESET_NAME}`]);
});
