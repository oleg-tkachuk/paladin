// Repository settings that live in the GitHub API, not in the codebase.
// Idempotent; safe to run again.
//
// Usage: GH_TOKEN=$(gh auth token) node .github/ci/repo-settings.ts
//        GH_TOKEN=… REQUIRE_CHECKS=1 node .github/ci/repo-settings.ts
//
// Run by hand, by a repository admin; no workflow runs it. It:
//
//   1. Leaves the repository's visibility untouched: making a repository
//      public is a one-way door and belongs to a person. The current value is
//      printed, so the run says which profile the rest assumes.
//   2. Deletes a pull request's head branch on merge: with main the only
//      long-lived branch, a merged branch has nothing left to do.
//   3. Enables private vulnerability reporting and Dependabot alerts.
//      SECURITY.md sends reporters to the private form, which 404s without it.
//   4. Creates or updates a branch ruleset protecting main.
//
// Rulesets rather than classic branch protection: they are additive, never
// clobbering protection set elsewhere, and work on free repositories.
//
// REQUIRED STATUS CHECKS are off unless REQUIRE_CHECKS=1: a required check
// that never reports blocks every merge, and this repository has been in that
// state, with Actions runs failing at startup on an exhausted minutes
// allowance. They land on main (CHECKS_REFS to change it), in a ruleset of
// their own: a ruleset carries one ref condition for all its rules. A required
// check also refuses direct pushes, whose commit has no check runs yet, which
// is how trunk-based work reaches main anyway.

import { Octokit } from "octokit";

import { info, warning } from "./lib/actions.ts";
import { optionalEnv, requireEnv } from "./lib/env.ts";
import { parseRepo, type Repo } from "./lib/github.ts";
import { runMain } from "./lib/main.ts";

export const DEFAULT_REPOSITORY = "oleg-tkachuk/paladin";
export const RULESET_NAME = "protect-main";
// What the ruleset was called while it also covered `develop`: looked up so a
// run renames and narrows that one in place instead of adding a second.
export const LEGACY_RULESET_NAME = "protect-main-develop";
export const CHECKS_RULESET_NAME = "require-checks-main";
export const MAIN_REF = "refs/heads/main";
// REQUIRE_CHECKS_ON turns the required checks on.
const REQUIRE_CHECKS_ON = "1";

// REQUIRED_CONTEXTS are check-run names, which are the jobs' `name:` in
// ci.yaml, not their ids; repo-settings.test.ts holds them to it. A context
// no job reports would block merges for good. A documentation-only change
// skips these jobs, and GitHub counts a skipped required check as passing.
export const REQUIRED_CONTEXTS: readonly string[] = ["Verify", "Workflow syntax", "Workflow audit"];

// ADMIN_ROLE_ID is the built-in admin RepositoryRole: a repository outside an
// organisation has no per-user actor, so this is how its owner is named.
export const ADMIN_ROLE_ID = 5;
// "always", not "pull_request": the narrower mode covers a merge through a
// pull request and nothing else, so a direct push to main is still refused.
// Measured, not assumed: the rule-suite audit recorded `bypass` for the
// merges and `fail` for the pushes.
const BYPASS_MODE = "always";

export interface Ruleset {
  name: string;
  target: "branch";
  enforcement: "active";
  bypass_actors?: { actor_id: number; actor_type: "RepositoryRole"; bypass_mode: "always" }[];
  conditions: { ref_name: { include: string[]; exclude: string[] } };
  rules: ({ type: "deletion" } | { type: "required_status_checks"; parameters: { strict_required_status_checks_policy: boolean; required_status_checks: { context: string }[] } })[];
}

// mainRuleset protects main from deletion. To also block force-pushes, add
// { type: "non_fast_forward" }.
export function mainRuleset(): Ruleset {
  return {
    name: RULESET_NAME,
    target: "branch",
    enforcement: "active",
    conditions: { ref_name: { include: [MAIN_REF], exclude: [] } },
    rules: [{ type: "deletion" }],
  };
}

// checksRuleset requires contexts on refs. The branch must be up to date with
// its base, so a check cannot pass against a stale tree; the admin bypasses
// it, so a check that cannot run never locks main against its maintainer.
export function checksRuleset(refs: readonly string[], contexts: readonly string[] = REQUIRED_CONTEXTS): Ruleset {
  return {
    name: CHECKS_RULESET_NAME,
    target: "branch",
    enforcement: "active",
    bypass_actors: [{ actor_id: ADMIN_ROLE_ID, actor_type: "RepositoryRole", bypass_mode: BYPASS_MODE }],
    conditions: { ref_name: { include: [...refs], exclude: [] } },
    rules: [
      {
        type: "required_status_checks",
        parameters: { strict_required_status_checks_policy: true, required_status_checks: contexts.map((context) => ({ context })) },
      },
    ],
  };
}

// splitRefs reads CHECKS_REFS: refs separated by white space.
export function splitRefs(text: string | undefined): string[] {
  const refs = (text ?? MAIN_REF).split(/\s+/).filter((r) => r !== "");
  return refs.length === 0 ? [MAIN_REF] : refs;
}

// RulesetsApi is what the upsert asks of the API.
export interface RulesetsApi {
  list(): Promise<{ id: number; name: string }[]>;
  create(ruleset: Ruleset): Promise<void>;
  update(id: number, ruleset: Ruleset): Promise<void>;
}

// upsertRuleset updates the ruleset carrying its name, or one of formerNames,
// and creates it when none does. It reports what it did.
export async function upsertRuleset(api: RulesetsApi, ruleset: Ruleset, formerNames: readonly string[] = []): Promise<string> {
  const existing = await api.list();
  for (const name of [ruleset.name, ...formerNames]) {
    const found = existing.find((r) => r.name === name);
    if (found !== undefined) {
      await api.update(found.id, ruleset);
      return `updated ruleset #${found.id} as ${ruleset.name}`;
    }
  }
  await api.create(ruleset);
  return `created ruleset ${ruleset.name}`;
}

// rulesetsOver is the RulesetsApi of repo through octokit.
function rulesetsOver(octokit: Octokit, repo: Repo): RulesetsApi {
  return {
    async list() {
      const { data } = await octokit.rest.repos.getRepoRulesets({ ...repo });
      return data.map((r) => ({ id: r.id, name: r.name }));
    },
    async create(ruleset) {
      await octokit.rest.repos.createRepoRuleset({ ...repo, ...ruleset });
    },
    async update(id, ruleset) {
      await octokit.rest.repos.updateRepoRuleset({ ...repo, ruleset_id: id, ...ruleset });
    },
  };
}

// tryEnable runs one enabling call; one already on, or not offered to this
// repository, is a warning, not a failure.
async function tryEnable(what: string, call: () => Promise<unknown>): Promise<void> {
  try {
    await call();
    info(`    ${what}: enabled`);
  } catch (err) {
    warning(`${what}: could not enable (already on, or not available here): ${err instanceof Error ? err.message : String(err)}`);
  }
}

async function main(): Promise<void> {
  const repo = parseRepo(optionalEnv("REPO") ?? DEFAULT_REPOSITORY);
  const octokit = new Octokit({ auth: requireEnv("GH_TOKEN") });

  const { data } = await octokit.rest.repos.get({ ...repo });
  info(`Repository: ${repo.owner}/${repo.repo}`);
  info(`Visibility (left unchanged): ${data.visibility ?? "unknown"}`);

  info("==> Deleting head branches on merge");
  const { data: updated } = await octokit.rest.repos.update({ ...repo, delete_branch_on_merge: true });
  info(`    delete_branch_on_merge = ${String(updated.delete_branch_on_merge)}`);

  info("==> Private vulnerability reporting and Dependabot alerts");
  await tryEnable("private vulnerability reporting", () => octokit.rest.repos.enablePrivateVulnerabilityReporting({ ...repo }));
  await tryEnable("Dependabot alerts", () => octokit.rest.repos.enableVulnerabilityAlerts({ ...repo }));

  const rulesets = rulesetsOver(octokit, repo);
  info("==> Branch ruleset for main");
  info(`    ${await upsertRuleset(rulesets, mainRuleset(), [LEGACY_RULESET_NAME])}`);

  if (optionalEnv("REQUIRE_CHECKS") === REQUIRE_CHECKS_ON) {
    const refs = splitRefs(optionalEnv("CHECKS_REFS"));
    info(`==> Required status checks on ${refs.join(" ")}: ${REQUIRED_CONTEXTS.join(", ")}`);
    info(`    ${await upsertRuleset(rulesets, checksRuleset(refs))}`);
  } else {
    info("==> Required status checks not applied (REQUIRE_CHECKS=1 once CI is green)");
  }
  info("Done.");
}

if (import.meta.main) {
  await runMain(main);
}
