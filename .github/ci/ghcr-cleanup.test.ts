import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { parse } from "yaml";

import { DELETE_TAGS_SEPARATOR, DEV_BUILDS_KEPT, Output, cleanupPlan, cleanupSummary } from "./ghcr-cleanup.ts";
import { Kind } from "./publish-plan.ts";

const plan = cleanupPlan("paladin-core", "charts/paladin-core");

test("both packages, the newest dev build kept, deleting for real", () => {
  assert.equal(plan[Output.Packages], "paladin-core,charts/paladin-core");
  assert.equal(plan[Output.Keep], "1");
  assert.equal(plan[Output.DryRun], "false");
  assert.throws(() => cleanupPlan("paladin-core", ""), /both/);
});

// The action matches tags with shell-style wildcards, any of its
// comma-separated patterns.
const shellMatch = (patterns: string, text: string) =>
  patterns
    .split(DELETE_TAGS_SEPARATOR)
    .some((pattern) => new RegExp(`^${pattern.split("*").map((p) => p.replace(/[.+?^${}()|[\]\\]/g, "\\$&")).join(".*")}$`).test(text));

test("dev builds only", () => {
  const glob = plan[Output.DeleteTags] ?? "";
  // After a release, and after a release candidate.
  for (const tag of ["38.1.1-dev.1792353836.gdab042fc", "0.1.0-dev.1.gabcdef12", "39.3.0-rc.1.dev.1792353836.gdab042fc"]) {
    assert.ok(shellMatch(glob, tag), tag);
  }
  for (const tag of ["38.1.0", "38.1.1", "v38.1.0", "39.3.0-rc.1", "sha256-eada85d13ca39a2939bf21479bfa2e6303f480a7ecd8f4997f3574eec93e2a12"]) {
    assert.ok(!shellMatch(glob, tag), tag);
  }
});

test("release.yaml cleans up after a dev build only, with the plan unchanged", () => {
  const workflow = parse(readFileSync(new URL("../workflows/release.yaml", import.meta.url), "utf8")) as {
    jobs: { publish: { steps: { name?: string; id?: string; if?: string; with?: Record<string, string> }[] } };
  };
  const step = (name: string) => workflow.jobs.publish.steps.find((s) => s.name === name);
  const planStep = step("Plan the cleanup of older dev builds");
  for (const name of ["Plan the cleanup of older dev builds", "Delete older dev builds"]) {
    assert.equal(step(name)?.if, `env.KIND == '${Kind.Dev}'`, name);
  }
  const inputs: Record<string, string> = {
    packages: Output.Packages,
    "delete-tags": Output.DeleteTags,
    "keep-n-tagged": Output.Keep,
    "dry-run": Output.DryRun,
  };
  for (const [input, output] of Object.entries(inputs)) {
    assert.equal(step("Delete older dev builds")?.with?.[input], `\${{ steps.${planStep?.id}.outputs.${output} }}`, input);
  }
});

test("cleanupSummary names both packages and what it keeps", () => {
  const summary = cleanupSummary("core", "charts/core");
  assert.ok(summary.includes("`core` and `charts/core`"), summary);
  assert.ok(summary.includes(`newest ${DEV_BUILDS_KEPT}`), summary);
});
